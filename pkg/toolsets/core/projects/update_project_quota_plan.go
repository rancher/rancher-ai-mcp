package projects

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

// updateProjectQuotaPlan plans an update of the resource quota of a project.
// It returns the JSON merge patch that would be applied without actually applying it.
func (t *Tools) updateProjectQuotaPlan(ctx context.Context, _ *mcp.CallToolRequest, params updateProjectQuotaParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("updateProjectQuotaPlan called", zap.String("cluster", params.Cluster), zap.String("project", params.Project))

	update, err := t.resolveQuotaUpdate(ctx, params)
	if err != nil {
		zap.L().Error("failed to resolve quota update", zap.String("tool", "updateProjectQuotaPlan"), zap.Error(err))
		return nil, nil, err
	}

	var mcpResponse string
	if update.noChange {
		mcpResponse, err = response.CreateNoChangesPlanResponse("The project already has the requested quota.")
	} else {
		mcpResponse, err = response.CreatePlanResponse([]response.PlanResource{
			response.NewUpdateResourceInput(update.project, update.patch, LocalCluster),
		})
	}

	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "updateProjectQuotaPlan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
