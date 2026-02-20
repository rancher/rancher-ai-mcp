package fleet

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	toolsSet    = "fleet"
	toolsSetAnn = "toolset"
	urlHeader   = "R_url"
)

type toolsClient interface {
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
}

// Tools contains all tools for the MCP server
type Tools struct {
	client     toolsClient
	RancherURL string
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, rancherURL string) *Tools {
	return &Tools{
		client:     client,
		RancherURL: rancherURL,
	}
}

func (t *Tools) rancherURL(toolReq *mcp.CallToolRequest) string {
	if t.RancherURL == "" {
		return toolReq.Extra.Header.Get(urlHeader)
	}

	return t.RancherURL
}

// AddTools registers all Rancher Kubernetes tools with the provided MCP server.
// Each tool is configured with metadata identifying it as part of the rancher toolset.
func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listGitRepos",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List GitRepos.
		Parameters:
		workspace (string, required): The workspace of the GitRepos.
		
		Returns:
		List of all GitRepos in the workspace.`},
		t.listGitRepos,
	)
}
