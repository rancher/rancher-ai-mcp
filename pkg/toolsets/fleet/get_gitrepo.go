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

// getGitRepoParams specifies the parameters needed to retrieve a specific Fleet GitRepo resource.
type getGitRepoParams struct {
	Name      string `json:"name" jsonschema:"the name of the GitRepo"`
	Workspace string `json:"workspace" jsonschema:"the workspace (namespace) of the GitRepo"`
}

// getGitRepo retrieves a specific Fleet GitRepo resource by name and workspace.
// The function queries the 'local' cluster where Fleet runs and returns the GitRepo
// in the specified workspace namespace, along with UI context for building resource links.
func (t *Tools) getGitRepo(ctx context.Context, toolReq *mcp.CallToolRequest, params getGitRepoParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getGitRepo called")

	gitRepo, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   "local",
		Kind:      "gitrepo",
		Namespace: params.Workspace,
		Name:      params.Name,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get gitrepo", zap.String("tool", "getGitRepo"), zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{gitRepo}, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "getGitRepo"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
