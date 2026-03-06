package core

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

var zapListClusters = zap.String("tool", "listClusters")

// listClusters retrieves a list of clusters in the management cluster.
func (t *Tools) listClusters(ctx context.Context, toolReq *mcp.CallToolRequest, params struct{}) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listClusters called")

	resources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: LocalCluster,
		Kind:    converter.ManagementClusterResourceKind,
		URL:     t.rancherURL(toolReq),
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list clusters", zapListClusters, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse(resources, LocalCluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListClusters, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
