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
	toolsSet    = "rancher"
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
		Name: "listProjectRoleTemplateBindings",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List all project role template bindings (PRTBs) in a Rancher cluster.
		If a user ID is specified only returns PRTBs for that user.
		If a project ID is specified only returns PRTBs for that project.
		PRTBs provide users permissions as specified by a RoleTemplate in a project.`},
		t.listProjectRoleTemplateBindings,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listRoleTemplates",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List all role templates in a Rancher cluster.
		Role templates define a set of permissions that can be assigned to users or groups.`},
		t.listRoleTemplates,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getUser",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Get a user ID by username.`},
		t.getUser,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getRoleTemplate",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Get a role template by name.`},
		t.getRoleTemplate,
	)
}
