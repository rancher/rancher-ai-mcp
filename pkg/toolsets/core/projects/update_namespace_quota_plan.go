package projects

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

// updateNamespaceQuotaPlan plans an update of the resource quota of a namespace.
// It returns the JSON merge patch that would be applied without actually applying it.
func (t *Tools) updateNamespaceQuotaPlan(ctx context.Context, _ *mcp.CallToolRequest, params updateNamespaceQuotaParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("updateNamespaceQuotaPlan called", zap.String("cluster", params.Cluster), zap.String("namespace", params.Namespace))

	update, err := t.resolveNamespaceQuotaUpdate(ctx, params)
	if err != nil {
		zap.L().Error("failed to resolve namespace quota update", zap.String("tool", "updateNamespaceQuotaPlan"), zap.Error(err))
		return nil, nil, err
	}

	var mcpResponse string
	if update.noChange {
		mcpResponse, err = response.CreateNoChangesPlanResponse("The namespace already has the requested quota.")
	} else {
		mcpResponse, err = response.CreatePlanResponse([]response.PlanResource{
			response.NewUpdateResourceInput(update.namespace, update.patch, update.clusterID),
		})
	}

	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "updateNamespaceQuotaPlan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
