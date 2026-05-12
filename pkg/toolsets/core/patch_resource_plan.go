package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// updateKubernetesResourcePlan plans an update to a Kubernetes resource using a JSON patch.
// It returns the original resource, the patch, and what the resource would look like after applying the patch.
func (t *Tools) updateKubernetesResourcePlan(ctx context.Context, toolReq *mcp.CallToolRequest, params updateKubernetesResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("updateKubernetesResource_plan called")

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), t.rancherURL(), params.Namespace, params.Cluster, converter.K8sKindsToGVRs[strings.ToLower(params.Kind)])
	if err != nil {
		return nil, nil, err
	}

	// Get the original resource
	original, err := resourceInterface.Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		zap.L().Error("failed to get original resource", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to get resource %s: %w", params.Name, err)
	}

	// Marshal the patch and original resource
	patchBytes, err := json.Marshal(params.Patch)
	if err != nil {
		zap.L().Error("failed to marshal patch", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	originalBytes, err := json.Marshal(original.Object)
	if err != nil {
		zap.L().Error("failed to marshal original resource", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal original: %w", err)
	}

	// Apply the patch to show the future state (without persisting)
	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		zap.L().Error("failed to decode patch", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to decode patch: %w", err)
	}

	patchedBytes, err := patch.Apply(originalBytes)
	if err != nil {
		zap.L().Error("failed to apply patch", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to apply patch: %w", err)
	}

	// Unmarshal the patched result
	var patchedObj map[string]any
	if err := json.Unmarshal(patchedBytes, &patchedObj); err != nil {
		zap.L().Error("failed to unmarshal patched resource", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to unmarshal patched: %w", err)
	}

	payload := map[string]any{
		"original": original.Object,
		"patch":    params.Patch,
		"patched":  patchedObj,
	}

	// Build plan resources
	planResources := []response.PlanResource{
		{
			Type:    response.OperationUpdate,
			Payload: payload,
			Resource: response.Resource{
				Name:      params.Name,
				Kind:      params.Kind,
				Cluster:   params.Cluster,
				Namespace: params.Namespace,
			},
		},
	}

	mcpResponse, err := response.CreatePlanResponse(planResources)
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
