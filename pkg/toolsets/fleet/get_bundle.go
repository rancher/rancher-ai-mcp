package fleet

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// getBundleParams specifies the parameters needed to retrieve a specific Fleet Bundle resource.
type getBundleParams struct {
	Name      string `json:"name" jsonschema:"the name of the Bundle"`
	Workspace string `json:"workspace" jsonschema:"the workspace (namespace) of the Bundle"`
}

// getBundle retrieves a specific Fleet Bundle resource by name and workspace.
// The function queries the 'local' cluster where Fleet runs and returns the Bundle
// in the specified workspace namespace, along with UI context for building resource links.
func (t *Tools) getBundle(ctx context.Context, toolReq *mcp.CallToolRequest, params getBundleParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getBundle called")

	bundle, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   "local",
		Kind:      "bundle",
		Namespace: params.Workspace,
		Name:      params.Name,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get bundle", zap.String("tool", "getBundle"), zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{bundle}, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "getBundle"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
