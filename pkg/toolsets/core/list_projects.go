package core

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

var zapListProjects = zap.String("tool", "listProjects")

type listProjectsParams struct {
	Cluster string `json:"cluster" jsonschema:"the name of the cluster resource the project belongs to"`
}

// listProjects retrieves a list of projects in a cluster.
func (t *Tools) listProjects(ctx context.Context, toolReq *mcp.CallToolRequest, params listProjectsParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listProjects called")

	resources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   LocalCluster,
		Kind:      "project",
		Namespace: params.Cluster,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list projects", zapListProjects, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse(resources, params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListProjects, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
