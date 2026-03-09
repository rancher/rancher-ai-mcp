package provisioning

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
)

func (t *Tools) createK3kClusterPlan(_ context.Context, toolReq *mcp.CallToolRequest, params createK3kClusterParams) (*mcp.CallToolResult, any, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"clusterName":   params.Name,
		"namespace":     params.Namespace,
		"targetCluster": params.TargetCluster,
	})

	log.Debug("Planning K3k cluster creation")

	if params.Name == "" {
		return nil, nil, fmt.Errorf("name is required")
	}
	if params.Namespace == "" {
		return nil, nil, fmt.Errorf("namespace is required")
	}
	if params.TargetCluster == "" {
		return nil, nil, fmt.Errorf("targetCluster is required")
	}

	obj := t.createK3kClusterObj(params)

	createResource := response.NewCreateResourceInput(obj, params.TargetCluster)
	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{createResource})
	if err != nil {
		log.Error("failed to create plan response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
