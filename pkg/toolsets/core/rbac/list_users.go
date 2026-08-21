package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

var zapListUsers = zap.String("tool", "listUsers")

// listUsers lists all Rancher user resources.
func (t *Tools) listUsers(ctx context.Context, toolReq *mcp.CallToolRequest, params struct{}) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listUsers called")

	users, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "user",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list users", zapListUsers, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse(users, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListUsers, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
