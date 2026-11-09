package projects

import (
	"context"
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

// moveNamespacePlan plans moving a namespace to a project.
// It returns the original namespace, the JSON patch, and what the namespace would look like after applying the patch,
// without actually applying it.
func (t *Tools) moveNamespacePlan(ctx context.Context, _ *mcp.CallToolRequest, params moveNamespaceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("moveNamespacePlan called", zap.String("namespace", params.Namespace), zap.String("project", params.Project), zap.String("cluster", params.Cluster))

	target, err := t.resolveMoveNamespace(ctx, params)
	if err != nil {
		zap.L().Error("failed to resolve namespace move", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, err
	}

	patchOps := target.patch()
	patchBytes, err := json.Marshal(patchOps)
	if err != nil {
		zap.L().Error("failed to marshal patch", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	originalBytes, err := json.Marshal(target.namespace.Object)
	if err != nil {
		zap.L().Error("failed to marshal original namespace", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal original: %w", err)
	}

	// Apply the patch to show the future state (without persisting)
	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		zap.L().Error("failed to decode patch", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to decode patch: %w", err)
	}

	patchedBytes, err := patch.Apply(originalBytes)
	if err != nil {
		zap.L().Error("failed to apply patch", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to apply patch: %w", err)
	}

	var patchedObj map[string]any
	if err := json.Unmarshal(patchedBytes, &patchedObj); err != nil {
		zap.L().Error("failed to unmarshal patched namespace", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to unmarshal patched: %w", err)
	}

	payload := map[string]any{
		"original": target.namespace.Object,
		"patch":    patchOps,
		"patched":  patchedObj,
	}

	planResources := []response.PlanResource{
		{
			Type:    response.OperationUpdate,
			Payload: payload,
			Resource: response.Resource{
				Name:    target.namespace.GetName(),
				Kind:    "Namespace",
				Cluster: target.clusterID,
			},
		},
	}

	mcpResponse, err := response.CreatePlanResponse(planResources)
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
