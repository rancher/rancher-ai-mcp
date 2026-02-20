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
	Cluster string `json:"cluster" jsonschema:"the cluster of the project resource"`
}

// listProjects retrieves a project resource.
func (t *Tools) listProjects(ctx context.Context, toolReq *mcp.CallToolRequest, params listProjectsParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listProjects called")

	resources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   "local",
		Kind:      "project",
		Namespace: params.Cluster,
		URL:       toolReq.Extra.Header.Get(urlHeader),
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
