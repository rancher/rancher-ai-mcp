package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

var zapListGlobalRoles = zap.String("tool", "listGlobalRoles")

// listGlobalRoles lists global role resources.
func (t *Tools) listGlobalRoles(ctx context.Context, toolReq *mcp.CallToolRequest, params struct{}) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listGlobalRoles called")

	globalRoles, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "globalrole",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list global roles", zapListGlobalRoles, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse(globalRoles, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListGlobalRoles, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
