package provisioning

import (
	"context"
	"fmt"

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

	// The plan may change before the user confirms the action, so do not show its exact hash.
	_, _, planErr := getMachinePlanFields(ctx, params.target, t.client, log)
	planHash := "PLAN_HASH"
	if planErr != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		log.Warn("machine plan unavailable, previewing recent system-agent logs", zap.Error(planErr))
		planHash = ""
	}

	job, err := buildFailedPlanLogGathererJob(params, t.toolboxImage, planHash, log)
	if err != nil {
		log.Error("failed to create preview systemd job", zap.Error(err))
		return nil, nil, err
	}

	plannedJob := response.NewCreateResourceInput(job, params.Cluster)
	plannedJob.Resource.Name = job.GetGenerateName() + "<generated>"
	if planErr != nil {
		plannedJob.Detail = fmt.Sprintf("Machine plan unavailable: %v. The job will retrieve recent system-agent logs without filtering by plan hash.", planErr)
	}

	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{plannedJob})
	if err != nil {
		log.Error("failed to create plan response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
