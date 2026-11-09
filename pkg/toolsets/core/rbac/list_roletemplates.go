package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
)

var zapListRoleTemplates = zap.String("tool", "listRoleTemplates")

// listRoleTemplates lists role template resources.
func (t *Tools) listRoleTemplates(ctx context.Context, toolReq *mcp.CallToolRequest, params struct{}) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listRoleTemplates called")

	roleTemplates, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "roletemplate",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list role templates", zapListRoleTemplates, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := createResourcesResponse(roleTemplates)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListRoleTemplates, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
