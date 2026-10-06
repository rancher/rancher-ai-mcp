package provisioning

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

func (t *Tools) createImportedClusterPlan(_ context.Context, toolReq *mcp.CallToolRequest, params createImportedClusterParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Planning imported cluster creation")

	cluster, err := t.createImportedClusterObj(params)
	if err != nil {
		log.Error("failed to plan imported cluster creation", zap.Error(err))
		return nil, nil, fmt.Errorf("failed to plan imported cluster creation: %w", err)
	}

	// While not required for the norman API request to create the cluster,
	// we depend on these fields to show the confirmation message
	// in the Rancher UI.
	cluster.SetKind("Cluster")
	cluster.SetAPIVersion(converter.ManagementGroup + "/v3")
	cluster.SetNamespace("fleet-default")
	cluster.SetName(params.Name)

	createResource := response.NewCreateResourceInput(cluster, LocalCluster)
	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{createResource})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
