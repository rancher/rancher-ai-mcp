package fleet

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

const (
	toolsSet    = "fleet"
	toolsSetAnn = "toolset"
	urlHeader   = "R_url"
)

type toolsClient interface {
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
}

type resourceAnalyzer interface {
	analyzeFleetResources(ctx context.Context, restCfg *rest.Config, namespace string) (string, error)
}

// Tools contains all tools for the MCP server
type Tools struct {
	client           toolsClient
	RancherURL       string
	resourceAnalyzer resourceAnalyzer
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, rancherURL string) *Tools {
	return &Tools{
		client:           client,
		RancherURL:       rancherURL,
		resourceAnalyzer: newCLI(),
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
		Name: "getBundle",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Get a specific Fleet Bundle by name.
		Parameters:
		name (string, required): The name of the Bundle.
		workspace (string, required): The workspace (namespace) of the Bundle.

		Returns:
		The Bundle resource matching the given name and workspace.`},
		t.getBundle,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getGitRepo",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Get a specific GitRepo by name.
		Parameters:
		name (string, required): The name of the GitRepo.
		workspace (string, required): The workspace (namespace) of the GitRepo.

		Returns:
		The GitRepo resource matching the given name and workspace.`},
		t.getGitRepo,
	)
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
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "analyzeFleetResources",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Analyze Fleet resources and diagnose bundle deployment issues.

		This command collects diagnostic information about Fleet resources including GitRepos,
		Bundles, BundleDeployments, and related resources. It outputs JSON containing only the
		fields relevant for troubleshooting, making it easy to identify issues like:

		• Bundles stuck with old commits or forceSyncGeneration
		• BundleDeployments not applying their target deploymentID
		• Orphaned secrets with invalid owner references
		• Resources stuck with deletion timestamps due to finalizers
		• API server consistency issues (time travel)
		• Missing or problematic Content resources
		
		Parameters:
				workspace (string, required): The workspace of the fleet resources to analyze. `},
		t.analyzeFleetResources,
	)
}
