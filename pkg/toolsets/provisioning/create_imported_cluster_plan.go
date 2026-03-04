package provisioning

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
)

func (t *Tools) createImportedClusterPlan(_ context.Context, toolReq *mcp.CallToolRequest, params createImportedClusterParams) (*mcp.CallToolResult, any, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"clusterName":              params.ClusterName,
		"clusterDescription":       params.ClusterDescription,
		"versionManagementSetting": params.VersionManagementSetting,
	})

	log.Debug("Planning imported cluster creation")

	unstructuredObj, err := t.createImportedClusterObj(params)
	if err != nil {
		log.Error("failed to plan imported cluster creation", zap.Error(err))
		return nil, nil, fmt.Errorf("failed to plan imported cluster creation: %w", err)
	}

	createResource := response.NewCreateResourceInput(unstructuredObj, LocalCluster)
	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{createResource})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
