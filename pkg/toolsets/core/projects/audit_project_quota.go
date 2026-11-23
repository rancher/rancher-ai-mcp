package projects

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapAuditProjectQuota = zap.String("tool", "auditProjectQuota")

const (
	// highUtilization is the fraction of a limit above which a resource is reported as nearly exhausted.
	highUtilization = 0.9
	// lowUtilization is the fraction of the allocated quota below which a resource is reported as over-provisioned.
	lowUtilization = 0.25
)

type auditProjectQuotaParams struct {
	Cluster string `json:"cluster" jsonschema:"the name of the cluster that the project belongs to"`
	Project string `json:"project" jsonschema:"the name or ID of the project to audit"`
}

// quotaSums holds the total hard limit and usage of a resource over the namespaces of a project.
type quotaSums struct {
	allocated resource.Quantity
	used      resource.Quantity
}

// auditProjectQuota compares the quota of a project with what is allocated to its namespaces and what they use.
func (t *Tools) auditProjectQuota(ctx context.Context, _ *mcp.CallToolRequest, params auditProjectQuotaParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("auditProjectQuota called", zap.String("cluster", params.Cluster), zap.String("project", params.Project))

	token := middleware.Token(ctx)

	clusterID, err := t.client.GetClusterID(ctx, token, params.Cluster)
	if err != nil {
		zap.L().Error("failed to get cluster ID", zapAuditProjectQuota, zap.Error(err))
		return nil, nil, err
	}

	projectID, project, err := GetProjectID(ctx, t.client, token, clusterID, params.Project)
	if err != nil {
		zap.L().Error("failed to get project", zapAuditProjectQuota, zap.Error(err))
		return nil, nil, err
	}

	displayName, _, _ := unstructured.NestedString(project.Object, "spec", "displayName")

	projectQuota := currentQuota(project, "spec", "resourceQuota", "limit")
	namespaceDefault := currentQuota(project, "spec", "namespaceDefaultResourceQuota", "limit")

	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{projectIDKey: projectID},
	})
	if err != nil {
		return nil, nil, err
	}

	namespaces, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       clusterID,
		Kind:          "namespace",
		LabelSelector: selector.String(),
		Token:         token,
	})
	if err != nil {
		zap.L().Error("failed to list namespaces", zapAuditProjectQuota, zap.Error(err))
		return nil, nil, err
	}

	sums := make(map[string]*quotaSums)
	namespaceSummary := []map[string]any{}
	withoutQuota := []string{}

	for _, ns := range namespaces {
		quotas, err := t.client.GetResources(ctx, client.ListParams{
			Cluster:   clusterID,
			Kind:      "resourcequota",
			Namespace: ns.GetName(),
			Token:     token,
		})
		if err != nil {
			zap.L().Error("failed to list resource quotas", zapAuditProjectQuota, zap.String("namespace", ns.GetName()), zap.Error(err))
			return nil, nil, fmt.Errorf("failed to list resource quotas of namespace %s: %w", ns.GetName(), err)
		}

		hard, used := namespaceQuota(quotas)
		if len(hard) == 0 {
			withoutQuota = append(withoutQuota, ns.GetName())
			continue
		}

		// Only report on resources covered by the project quota, if there is one.
		usage := map[string]string{}

		for _, key := range sortedKeys(hard) {
			if _, ok := projectQuota[key]; !ok && len(projectQuota) > 0 {
				continue
			}

			s := sums[key]
			if s == nil {
				s = &quotaSums{}
				sums[key] = s
			}

			s.allocated.Add(hard[key])
			s.used.Add(used[key])
			usage[key] = fmt.Sprintf("%s / %s", quantityString(used[key]), quantityString(hard[key]))
		}

		namespaceSummary = append(namespaceSummary, map[string]any{"namespace": ns.GetName(), "used/hard": usage})
	}

	slices.Sort(withoutQuota)
	slices.SortFunc(namespaceSummary, func(a, b map[string]any) int {
		return strings.Compare(a["namespace"].(string), b["namespace"].(string))
	})

	resources, findings := summarizeQuota(projectQuota, namespaceDefault, sums)
	if len(projectQuota) == 0 {
		findings = append(findings, "The project has no resource quota, so its namespaces can consume unlimited resources.")
	}

	if len(projectQuota) > 0 && len(withoutQuota) > 0 {
		findings = append(findings, fmt.Sprintf("Namespaces without a resource quota are not limited and don't count against the project quota: %v.", withoutQuota))
	}

	result := map[string]any{
		"cluster":     clusterID,
		"project":     projectID,
		"displayName": displayName,
		"resources":   resources,
		"namespaces":  namespaceSummary,
		"findings":    findings,
	}
	if len(withoutQuota) > 0 {
		result["namespacesWithoutQuota"] = withoutQuota
	}

	mcpResponse, err := response.CreateMcpResponseAny(result)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapAuditProjectQuota, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// namespaceQuota returns the hard limits and usage of a namespace, from its Kubernetes ResourceQuotas,
// keyed like the flat representation of a project quota. Usage of a resource covered by several
// ResourceQuotas isn't added up, as every quota counts the same usage; the highest is used instead.
func namespaceQuota(quotas []*unstructured.Unstructured) (hard, used map[string]resource.Quantity) {
	hard = map[string]resource.Quantity{}
	used = map[string]resource.Quantity{}

	byK8sName := map[string]string{}
	for key, name := range quotaFieldToK8sName {
		byK8sName[name] = key
	}

	for _, quota := range quotas {
		for _, entry := range []struct {
			path   string
			target map[string]resource.Quantity
		}{{"hard", hard}, {"used", used}} {
			values, _, _ := unstructured.NestedStringMap(quota.Object, "status", entry.path)
			for name, value := range values {
				q, err := resource.ParseQuantity(value)
				if err != nil {
					continue
				}

				key, ok := byK8sName[name]
				if !ok {
					key = name
				}

				// Overlapping quotas are enforced together, so the effective limit is the smallest hard value.
				// The largest used value is kept, as the same objects count towards each quota.
				if existing, found := entry.target[key]; !found || (entry.path == "hard") == (q.Cmp(existing) < 0) {
					entry.target[key] = q
				}
			}
		}
	}

	return hard, used
}

// summarizeQuota builds the per-resource summary of a project quota and findings worth acting on.
func summarizeQuota(projectQuota, namespaceDefault map[string]string, sums map[string]*quotaSums) ([]map[string]any, []string) {
	keys := map[string]struct{}{}
	for key := range projectQuota {
		keys[key] = struct{}{}
	}

	for key := range sums {
		keys[key] = struct{}{}
	}

	resources := []map[string]any{}
	findings := []string{}

	for _, key := range sortedKeys(keys) {
		entry := map[string]any{"resource": key}
		if v, ok := projectQuota[key]; ok {
			entry["projectLimit"] = v
		}

		if v, ok := namespaceDefault[key]; ok {
			entry["namespaceDefault"] = v
		}

		s := sums[key]
		if s == nil {
			s = &quotaSums{}
		}

		entry["allocated"] = quantityString(s.allocated)
		entry["used"] = quantityString(s.used)

		if v, ok := projectQuota[key]; ok {
			if limit, err := resource.ParseQuantity(v); err == nil && limit.MilliValue() > 0 {
				allocatedRatio := float64(s.allocated.MilliValue()) / float64(limit.MilliValue())

				entry["allocatedPercent"] = percent(allocatedRatio)
				if allocatedRatio >= highUtilization {
					findings = append(findings, fmt.Sprintf("%s: %.0f%% of the project limit is already allocated to namespaces, leaving little room for new namespaces or increases.", key, allocatedRatio*100))
				}
			}
		}

		if s.allocated.MilliValue() > 0 {
			usedRatio := float64(s.used.MilliValue()) / float64(s.allocated.MilliValue())

			entry["usedPercent"] = percent(usedRatio)
			switch {
			case usedRatio >= highUtilization:
				findings = append(findings, fmt.Sprintf("%s: namespaces use %.0f%% of what is allocated to them, so workloads may soon be rejected.", key, usedRatio*100))
			case usedRatio < lowUtilization:
				findings = append(findings, fmt.Sprintf("%s: namespaces use only %.0f%% of what is allocated to them, so the quota may be over-provisioned.", key, usedRatio*100))
			default:
				// Utilization is in the expected range, nothing to report.
			}
		}

		resources = append(resources, entry)
	}

	return resources, findings
}

func percent(ratio float64) float64 {
	return float64(int(ratio*1000+0.5)) / 10
}

func quantityString(q resource.Quantity) string {
	return q.String()
}
