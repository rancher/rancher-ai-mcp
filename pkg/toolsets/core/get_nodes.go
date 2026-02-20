package core

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

// getNodesParams specifies the parameters needed to retrieve node metrics.
type getNodesParams struct {
	Cluster string `json:"cluster" jsonschema:"the cluster of the resource"`
}

// getNodes retrieves information and metrics for all nodes in a given cluster.
func (t *Tools) getNodes(ctx context.Context, toolReq *mcp.CallToolRequest, params getNodesParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getNodes called")

	nodeResource, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: params.Cluster,
		Kind:    "node",
		URL:     t.rancherURL(toolReq),
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get nodes", zap.String("tool", "getNodes"), zap.Error(err))
		return nil, nil, err
	}

	// ignore error as Metrics Server might not be installed in the cluster
	nodeMetricsResource, _ := t.client.GetResources(ctx, client.ListParams{
		Cluster: params.Cluster,
		Kind:    "node.metrics.k8s.io",
		URL:     t.rancherURL(toolReq),
		Token:   middleware.Token(ctx),
	})

	mcpResponse, err := response.CreateMcpResponse(append(nodeResource, nodeMetricsResource...), params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "getNodes"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
