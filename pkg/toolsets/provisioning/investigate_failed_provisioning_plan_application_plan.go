package provisioning

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

func (t *Tools) investigateFailedPlanApplicationPlan(ctx context.Context, toolReq *mcp.CallToolRequest, params investigateFailedPlanApplicationParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(ctx, t, toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Planning failed plan execution investigation")

	// We don't show the exact plan hash in the preview as it may change before the user confirms this action in the UI.
	job, err := buildFailedPlanLogGathererJob(params, t.toolboxImage, "PLAN_HASH", log)
	if err != nil {
		log.Error("failed to create preview systemd job", zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{response.NewCreateResourceInput(job, params.Cluster)})
	if err != nil {
		log.Error("failed to create plan response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
