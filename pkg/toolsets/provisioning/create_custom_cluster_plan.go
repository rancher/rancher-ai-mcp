package provisioning

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
)

func (t *Tools) createCustomClusterPlan(_ context.Context, toolReq *mcp.CallToolRequest, params createCustomClusterParams) (*mcp.CallToolResult, any, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"clusterName":        params.ClusterName,
		"clusterDescription": params.ClusterDescription,
		"CNI":                params.CNI,
		"KubernetesVersion":  params.KubernetesVersion,
		"Distribution":       params.Distribution,
	})

	log.Debug("Planning custom cluster creation")

	unstructuredObj, err := t.CreateCustomClusterObj(toolReq, params, log)
	if err != nil {
		log.Error("failed to create custom cluster object", zap.Error(err))
		return nil, nil, err
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
