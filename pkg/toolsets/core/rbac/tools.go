package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	toolsSet    = "rancher"
	toolsSetAnn = "toolset"
)

type toolsClient interface {
	GetClusterID(ctx context.Context, token string, clusterNameOrID string) (string, error)
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	GetResourceInterface(ctx context.Context, token string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error)
	CreateClientSet(ctx context.Context, token string, cluster string) (kubernetes.Interface, error)
}

// Tools contains tools for interacting with RBAC in Rancher.
type Tools struct {
	client   toolsClient
	ReadOnly bool
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, readOnly bool) *Tools {
	return &Tools{
		client:   client,
		ReadOnly: readOnly,
	}
}

// AddTools registers all RBAC tools with the provided MCP server.
func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listClusterRoleTemplateBindings",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List all cluster role template bindings (CRTBs) in a Rancher cluster.
		If a user ID is specified only returns CRTBs for that user.
		CRTBs provide users permissions as specified by a RoleTemplate at the cluster level.`},
		t.listClusterRoleTemplateBindings,
	)
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
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
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
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "whoAmI",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns the identity of the currently authenticated Rancher user: their user ID, group memberships, and full user details.
		Use this to answer questions such as "who am I" or "which user/account am I logged in as".`},
		t.whoAmI,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listUsers",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List all users in Rancher, including their username, display name, principal IDs (authentication provider identities) and enabled status.`},
		t.listUsers,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listAuthConfigs",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List all authentication provider configurations in Rancher (e.g. local, Active Directory, LDAP, SAML, GitHub, OIDC).
		Each entry indicates whether it is enabled. Use this to determine which authentication mechanism(s) are active.`},
		t.listAuthConfigs,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listUserAttributes",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List login attributes for Rancher users, including the last login time.
		If a username or user ID is specified, only returns the attributes for that user; otherwise returns attributes for all users.
		Use this to find when a user last logged in, or to audit users that haven't logged in recently.`},
		t.listUserAttributes,
	)
}
