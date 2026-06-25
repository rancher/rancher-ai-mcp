package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	toolsSet    = "rbac"
	toolsSetAnn = "toolset"
	urlHeader   = "R_url"
)

type toolsClient interface {
	GetClusterID(ctx context.Context, token string, url string, clusterNameOrID string) (string, error)
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	GetResourceInterface(ctx context.Context, token string, url string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error)
}

// Tools contains tools for interacting with RBAC in Rancher.
type Tools struct {
	client     toolsClient
	RancherURL string
	ReadOnly   bool
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, rancherURL string, readOnly bool) *Tools {
	return &Tools{
		client:     client,
		RancherURL: rancherURL,
		ReadOnly:   readOnly,
	}
}

func (t *Tools) rancherURL(toolReq *mcp.CallToolRequest) string {
	if t.RancherURL == "" {
		return toolReq.Extra.Header.Get(urlHeader)
	}

	return t.RancherURL
}

// AddTools registers all RBAC tools with the provided MCP server.
func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listProjectPermissions",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Use this tool — NOT listKubernetesResources — when the user asks about a user's permissions, roles, or access rights in a Rancher project. Resolves project role template bindings (PRTBs) for the specified user, fetches the associated role templates, and returns the full set of policy rules granted to that user. If no project is specified, returns permissions across all projects in the given cluster.`},
		t.listProjectPermissions,
	)
}
