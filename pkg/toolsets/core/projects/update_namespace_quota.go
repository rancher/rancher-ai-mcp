package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

// namespaceQuotaAnnotation is the namespace annotation Rancher reads to give a namespace a quota other than
// the namespace default of its project. Its value is JSON of the form {"limit": {...}}.
const namespaceQuotaAnnotation = "field.cattle.io/resourceQuota"

type updateNamespaceQuotaParams struct {
	Cluster   string            `json:"cluster" jsonschema:"the name of the cluster that the namespace belongs to"`
	Namespace string            `json:"namespace" jsonschema:"the name of the namespace, which must belong to a project with a resource quota"`
	Quota     map[string]string `json:"quota" jsonschema:"the quota of the namespace, as resource name to quantity. Only the given resources are changed, others keep their value. Resource names: requestsCpu, limitsCpu, requestsMemory, limitsMemory, requestsStorage, pods, services, replicationControllers, secrets, configMaps, persistentVolumeClaims, servicesNodePorts, servicesLoadBalancers, or an extended resource such as nvidia.com/gpu. Each resource must already have a namespace default in the project, and the quotas of all namespaces together can't exceed the project quota"`
}

// namespaceQuotaUpdate holds the resolved data needed to update the quota of a namespace.
type namespaceQuotaUpdate struct {
	clusterID string
	namespace *unstructured.Unstructured
	patch     []byte
	noChange  bool
}

// resolveNamespaceQuotaUpdate resolves the namespace and its project, merges the requested quota with the current
// one, checks that the result fits in the project quota and builds the JSON merge patch that applies it.
func (t *Tools) resolveNamespaceQuotaUpdate(ctx context.Context, params updateNamespaceQuotaParams) (*namespaceQuotaUpdate, error) {
	if len(params.Quota) == 0 {
		return nil, errors.New("nothing to update: set quota")
	}

	requested, err := normalizeQuota(params.Quota)
	if err != nil {
		return nil, err
	}

	token := middleware.Token(ctx)

	clusterID, err := t.client.GetClusterID(ctx, token, params.Cluster)
	if err != nil {
		return nil, err
	}

	namespace, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: clusterID,
		Kind:    "namespace",
		Name:    params.Namespace,
		Token:   token,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace %q: %w", params.Namespace, err)
	}

	projectID := namespace.GetLabels()[projectIDKey]
	if projectID == "" {
		return nil, fmt.Errorf("namespace %q doesn't belong to a project, so it has no project quota", params.Namespace)
	}

	_, project, err := GetProjectID(ctx, t.client, token, clusterID, projectID)
	if err != nil {
		return nil, err
	}

	projectQuota := currentQuota(project, "spec", "resourceQuota", "limit")

	namespaceDefault := currentQuota(project, "spec", "namespaceDefaultResourceQuota", "limit")
	if len(projectQuota) == 0 || len(namespaceDefault) == 0 {
		return nil, fmt.Errorf("project %q has no resource quota: set one with updateProjectQuota first", projectID)
	}

	for _, key := range sortedKeys(requested) {
		if _, ok := namespaceDefault[key]; !ok {
			return nil, fmt.Errorf("resource %q has no namespace default in project %q: add it with updateProjectQuota first", key, projectID)
		}
	}

	current := effectiveNamespaceQuota(namespace, namespaceDefault)
	merged := maps.Clone(current)
	maps.Copy(merged, requested)

	if err := validateNamespaceQuotaFit(params.Namespace, merged, projectQuota, t.otherNamespaceQuotas(ctx, clusterID, projectID, params.Namespace, namespaceDefault)); err != nil {
		return nil, err
	}

	value, err := json.Marshal(map[string]any{"limit": unflattenLimit(merged)})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal namespace quota: %w", err)
	}

	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{namespaceQuotaAnnotation: string(value)}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	return &namespaceQuotaUpdate{
		clusterID: clusterID,
		namespace: namespace,
		patch:     patch,
		noChange:  maps.Equal(merged, current),
	}, nil
}

// effectiveNamespaceQuota returns the quota of a namespace: the one in its annotation, which Rancher keeps
// complete, or the namespace default of its project if it has none.
func effectiveNamespaceQuota(namespace *unstructured.Unstructured, namespaceDefault map[string]string) map[string]string {
	if annotation := namespace.GetAnnotations()[namespaceQuotaAnnotation]; annotation != "" {
		var quota struct {
			Limit map[string]any `json:"limit"`
		}
		if err := json.Unmarshal([]byte(annotation), &quota); err == nil && len(quota.Limit) > 0 {
			return flattenLimit(quota.Limit)
		}
	}

	return maps.Clone(namespaceDefault)
}

// otherNamespaceQuotas returns the effective quota of every other namespace of the project.
func (t *Tools) otherNamespaceQuotas(ctx context.Context, clusterID, projectID, namespace string, namespaceDefault map[string]string) []map[string]string {
	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{projectIDKey: projectID}})
	if err != nil {
		return nil
	}

	namespaces, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       clusterID,
		Kind:          "namespace",
		LabelSelector: selector.String(),
		Token:         middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Warn("failed to list namespaces of project, skipping the fit check against them", zap.String("project", projectID), zap.Error(err))
		return nil
	}

	var quotas []map[string]string

	for _, other := range namespaces {
		if other.GetName() != namespace {
			quotas = append(quotas, effectiveNamespaceQuota(other, namespaceDefault))
		}
	}

	return quotas
}

// validateNamespaceQuotaFit checks that the quota of a namespace together with the quotas of the other namespaces
// of the project doesn't exceed the project quota. Rancher silently ignores a quota that doesn't fit, so it is
// reported here instead.
func validateNamespaceQuotaFit(namespace string, quota, projectQuota map[string]string, others []map[string]string) error {
	for _, key := range sortedKeys(quota) {
		limitValue, ok := projectQuota[key]
		if !ok {
			continue
		}

		limit, err := resource.ParseQuantity(limitValue)
		if err != nil {
			return fmt.Errorf("invalid project quota for %q: %w", key, err)
		}

		total, err := resource.ParseQuantity(quota[key])
		if err != nil {
			return fmt.Errorf("invalid quota for %q: %w", key, err)
		}

		if total.Sign() < 0 {
			return fmt.Errorf("quota for %q can't be negative", key)
		}

		for _, other := range others {
			if value, ok := other[key]; ok {
				q, err := resource.ParseQuantity(value)
				if err != nil {
					continue
				}

				total.Add(q)
			}
		}

		if total.Cmp(limit) > 0 {
			return fmt.Errorf("the quotas of the namespaces would add up to %s of %q, exceeding the project quota of %s: lower the quota of %s or of the other namespaces, or raise the project quota with updateProjectQuota",
				total.String(), key, limitValue, namespace)
		}
	}

	return nil
}

// updateNamespaceQuota sets or updates the resource quota of a namespace in a project.
func (t *Tools) updateNamespaceQuota(ctx context.Context, _ *mcp.CallToolRequest, params updateNamespaceQuotaParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("updateNamespaceQuota called", zap.String("cluster", params.Cluster), zap.String("namespace", params.Namespace))

	update, err := t.resolveNamespaceQuotaUpdate(ctx, params)
	if err != nil {
		zap.L().Error("failed to resolve namespace quota update", zap.String("tool", "updateNamespaceQuota"), zap.Error(err))
		return nil, nil, err
	}

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx), "", update.clusterID, converter.K8sKindsToGVRs["namespace"])
	if err != nil {
		return nil, nil, err
	}

	updated, err := resourceInterface.Patch(ctx, update.namespace.GetName(), types.MergePatchType, update.patch, metav1.PatchOptions{})
	if err != nil {
		zap.L().Error("failed to patch namespace quota", zap.String("tool", "updateNamespaceQuota"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to update namespace quota: %w", err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{updated}, update.clusterID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create MCP response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
