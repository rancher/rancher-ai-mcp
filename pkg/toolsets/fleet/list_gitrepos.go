package fleet

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

// listGitRepoParams specifies the parameters needed to list Fleet GitRepo resources.
type listGitRepoParams struct {
	Workspace string `json:"workspace" jsonschema:"the workspace of the gitrepo"`
}

// listGitRepos retrieves all Fleet GitRepo resources for a specific workspace.
// The function queries the 'local' cluster where Fleet runs and returns all GitRepos
// in the specified workspace namespace, along with UI context for building resource links.
func (t *Tools) listGitRepos(ctx context.Context, toolReq *mcp.CallToolRequest, params listGitRepoParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listGitRepos called")

	gitRepos, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   "local",
		Kind:      "gitrepo",
		Namespace: params.Workspace,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list gitrepos", zap.String("tool", "listGitRepos"), zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse(gitRepos, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "listGitRepos"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
