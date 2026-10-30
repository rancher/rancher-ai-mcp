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
)

type toolsClient interface {
	GetClusterID(ctx context.Context, token string, clusterNameOrID string) (string, error)
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	GetResourceInterface(ctx context.Context, token string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error)
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

	if !t.ReadOnly {
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createProjectRoleTemplateBinding",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Grants a user or group a role in a project by creating a project role template binding (PRTB), e.g. to add a project member.
		Common project roles are project-owner, project-member and read-only. Defaults to project-member.
		If the user or group already has the role in the project, no changes are made.`},
			t.createProjectRoleTemplateBinding,
		)
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createProjectRoleTemplateBindingPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to grant a user or group a role in a project. It returns the JSON representation of the project role template binding (PRTB) to be created without actually creating it in the cluster. Only used for displaying the resource when using human validation.`},
			t.createProjectRoleTemplateBindingPlan,
		)
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteProjectRoleTemplateBinding",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Removes a role from a user or group in a project by deleting the matching project role template bindings (PRTBs), e.g. to remove a project member.
		Common project roles are project-owner, project-member and read-only. Defaults to project-member.
		If the user or group does not have the role in the project, no changes are made.`},
			t.deleteProjectRoleTemplateBinding,
		)
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteProjectRoleTemplateBindingPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to remove a role from a user or group in a project. It returns the project role template bindings (PRTBs) to be deleted without actually deleting them from the cluster. Only used for displaying the resource when using human validation.`},
			t.deleteProjectRoleTemplateBindingPlan,
		)
	}
}
