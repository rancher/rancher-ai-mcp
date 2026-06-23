package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapGetRoleTemplate = zap.String("tool", "getRoleTemplate")

type getRoleTemplateParams struct {
	Name string `json:"name" jsonschema:"the name of the role template to retrieve"`
}

// getRoleTemplate retrieves a role template resource.
func (t *Tools) getRoleTemplate(ctx context.Context, toolReq *mcp.CallToolRequest, params getRoleTemplateParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getRoleTemplate called", zap.String("name", params.Name))

	roleTemplate, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: "local",
		Kind:    "roletemplate",
		Name:    params.Name,
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get role template", zapGetRoleTemplate, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{roleTemplate}, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetRoleTemplate, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
