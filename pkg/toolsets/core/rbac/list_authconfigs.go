package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

var zapListAuthConfigs = zap.String("tool", "listAuthConfigs")

// listAuthConfigs lists all authentication provider configurations in Rancher.
func (t *Tools) listAuthConfigs(ctx context.Context, toolReq *mcp.CallToolRequest, params struct{}) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listAuthConfigs called")

	authConfigs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "authconfig",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list auth configs", zapListAuthConfigs, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse(authConfigs, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListAuthConfigs, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
