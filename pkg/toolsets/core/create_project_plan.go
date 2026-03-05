package core

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

// createProjectPlan plans the creation of a new project.
// It returns the JSON representation of the project to be created without actually creating it.
func (t *Tools) createProjectPlan(_ context.Context, _ *mcp.CallToolRequest, params createProjectParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createProject_plan called", zap.String("cluster", params.Cluster))

	project, err := t.createProjectObj(params)
	if err != nil {
		zap.L().Error("failed to create project object", zap.String("tool", "createProject_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create project object: %w", err)
	}

	createResource := response.NewCreateResourceInput(project, params.Cluster)
	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{createResource})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "createProject_plan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
