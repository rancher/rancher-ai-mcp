package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

// updateKubernetesResourcePlan plans an update to a Kubernetes resource using a JSON patch.
// It returns the JSON representation of the planned update without actually applying it.
func (t *Tools) updateKubernetesResourcePlan(_ context.Context, _ *mcp.CallToolRequest, params updateKubernetesResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("updateKubernetesResource_plan called")

	patchBytes, err := json.Marshal(params.Patch)
	if err != nil {
		zap.L().Error("failed to marshal patch", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	updateResource := response.PlanResource{
		Type:    response.OperationUpdate,
		Payload: json.RawMessage(patchBytes),
		Resource: response.Resource{
			Name:      params.Name,
			Kind:      params.Kind,
			Cluster:   params.Cluster,
			Namespace: params.Namespace,
		},
	}
	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{updateResource})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "updateKubernetesResource_plan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
