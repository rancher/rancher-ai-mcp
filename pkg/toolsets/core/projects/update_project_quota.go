package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

type updateProjectQuotaParams struct {
	Cluster               string            `json:"cluster" jsonschema:"the name of the cluster that the project belongs to"`
	Project               string            `json:"project" jsonschema:"the name or ID of the project"`
	ProjectQuota          map[string]string `json:"projectQuota,omitempty" jsonschema:"(optional) the total quota of the project, as resource name to quantity. Only the given resources are changed, others keep their value. Resource names: requestsCpu, limitsCpu, requestsMemory, limitsMemory, requestsStorage, pods, services, replicationControllers, secrets, configMaps, persistentVolumeClaims, servicesNodePorts, servicesLoadBalancers, or an extended resource such as nvidia.com/gpu. Quantities use Kubernetes units, e.g. 20 or 500m for CPU and 64Gi for memory"`
	NamespaceDefaultQuota map[string]string `json:"namespaceDefaultQuota,omitempty" jsonschema:"(optional) the default quota given to each namespace of the project, using the same resource names and units as projectQuota. Rancher requires it to cover every resource in projectQuota without exceeding it"`
}

// quotaUpdate holds the resolved data needed to update the quota of a project.
type quotaUpdate struct {
	clusterID string
	project   *unstructured.Unstructured
	patch     []byte
	noChange  bool
}

// resolveQuotaUpdate resolves the project, merges the requested quotas with the existing ones, validates the
// result and builds the JSON merge patch that applies it.
func (t *Tools) resolveQuotaUpdate(ctx context.Context, params updateProjectQuotaParams) (*quotaUpdate, error) {
	if len(params.ProjectQuota) == 0 && len(params.NamespaceDefaultQuota) == 0 {
		return nil, errors.New("nothing to update: set projectQuota and/or namespaceDefaultQuota")
	}

	newProject, err := normalizeQuota(params.ProjectQuota)
	if err != nil {
		return nil, err
	}

	newDefault, err := normalizeQuota(params.NamespaceDefaultQuota)
	if err != nil {
		return nil, err
	}

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		return nil, err
	}

	_, project, err := GetProjectID(ctx, t.client, middleware.Token(ctx), clusterID, params.Project)
	if err != nil {
		return nil, err
	}

	currentProject := currentQuota(project, "spec", "resourceQuota", "limit")
	currentDefault := currentQuota(project, "spec", "namespaceDefaultResourceQuota", "limit")

	mergedProject := maps.Clone(currentProject)
	maps.Copy(mergedProject, newProject)

	mergedDefault := maps.Clone(currentDefault)
	maps.Copy(mergedDefault, newDefault)

	if err := validateQuotaConsistency(mergedProject, mergedDefault); err != nil {
		return nil, err
	}

	spec := map[string]any{}
	if len(newProject) > 0 {
		spec["resourceQuota"] = map[string]any{"limit": unflattenLimit(newProject)}
	}

	if len(newDefault) > 0 {
		spec["namespaceDefaultResourceQuota"] = map[string]any{"limit": unflattenLimit(newDefault)}
	}

	patch, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	return &quotaUpdate{
		clusterID: clusterID,
		project:   project,
		patch:     patch,
		noChange:  maps.Equal(mergedProject, currentProject) && maps.Equal(mergedDefault, currentDefault),
	}, nil
}

// currentQuota returns the flat quota limit found at the given path of a project.
func currentQuota(project *unstructured.Unstructured, path ...string) map[string]string {
	limit, _, _ := unstructured.NestedMap(project.Object, path...)
	return flattenLimit(limit)
}

// updateProjectQuota sets or updates the resource quota of a project.
func (t *Tools) updateProjectQuota(ctx context.Context, _ *mcp.CallToolRequest, params updateProjectQuotaParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("updateProjectQuota called", zap.String("cluster", params.Cluster), zap.String("project", params.Project))

	update, err := t.resolveQuotaUpdate(ctx, params)
	if err != nil {
		zap.L().Error("failed to resolve quota update", zap.String("tool", "updateProjectQuota"), zap.Error(err))
		return nil, nil, err
	}

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx), update.clusterID, LocalCluster, converter.K8sKindsToGVRs["project"])
	if err != nil {
		return nil, nil, err
	}

	updated, err := resourceInterface.Patch(ctx, update.project.GetName(), types.MergePatchType, update.patch, metav1.PatchOptions{})
	if err != nil {
		zap.L().Error("failed to patch project quota", zap.String("tool", "updateProjectQuota"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to update project quota: %w", err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{updated}, update.clusterID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create MCP response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
