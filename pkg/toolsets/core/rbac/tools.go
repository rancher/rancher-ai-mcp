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
		If a user is specified only returns CRTBs for that user and the groups the user belongs to.
		If a group is specified only returns CRTBs for that group. A user and a group cannot both be specified.
		CRTBs provide users permissions as specified by a RoleTemplate at the cluster level.`},
		t.listClusterRoleTemplateBindings,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listProjectRoleTemplateBindings",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List all project role template bindings (PRTBs) in a Rancher cluster.
		If a user is specified only returns PRTBs for that user and the groups the user belongs to.
		If a group is specified only returns PRTBs for that group. A user and a group cannot both be specified.
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
		Description: `Get a user by username or user ID.
		The user ID is the metadata.name field of the user.`},
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
		Name: "listGlobalRoles",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Description: `List all global roles in Rancher.
		Global roles define permissions that apply across Rancher rather than to a specific cluster or project.`},
		t.listGlobalRoles,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getGlobalRole",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Get a global role by name or display name, including the rules and inherited cluster roles that define its permissions.
		Display names are matched case-insensitively. If more than one global role has the display name, all of them are returned.
		If no global role matches, the names and display names of all global roles are returned so the closest one can be suggested.`},
		t.getGlobalRole,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listGlobalRoleBindings",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List all global role bindings (GRBs) in Rancher.
		If a user is specified only returns GRBs for that user and the groups the user belongs to.
		If a group is specified only returns GRBs for that group. A user and a group cannot both be specified.
		If a global role is specified only returns GRBs for that global role.
		GRBs provide users or groups permissions as specified by a GlobalRole across Rancher.
		To find out everything a user can do, combine GRBs with their cluster and project role template bindings.`},
		t.listGlobalRoleBindings,
	)

	if !t.ReadOnly {
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createGlobalRole",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			InputSchema: createGlobalRoleInputSchema(),
			Description: `Creates a global role in Rancher.
			Rules grant permissions in the local cluster where Rancher runs.
			Inherited cluster roles grant the named cluster role templates in every downstream cluster.
			Use createGlobalRoleBinding to assign the global role to users or groups.`},
			t.createGlobalRole)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createGlobalRolePlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			InputSchema: createGlobalRoleInputSchema(),
			Description: `Plans to create a global role in Rancher. It returns the JSON representation of the global role to be created without actually creating it. Only used for displaying the resource when using human validation.`},
			t.createGlobalRolePlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteGlobalRole",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Deletes a global role by name. Built-in global roles cannot be deleted.
			Rancher also deletes every global role binding (GRB) for the global role, so all users and groups assigned the role lose it.
			Use getGlobalRole to find the name from a display name.`},
			t.deleteGlobalRole)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteGlobalRolePlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to delete a global role. It returns the JSON representation of the global role and the global role bindings that will be deleted with it, without actually deleting them. Only used for displaying the resources when using human validation.`},
			t.deleteGlobalRolePlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createGlobalRoleBinding",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Assigns a global role to a user or group by creating a global role binding (GRB).
			Exactly one of user or group must be specified. The user can be a username or user ID.
			The global role must be specified by name. Use getGlobalRole to find the name from a display name.`},
			t.createGlobalRoleBinding)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createGlobalRoleBindingPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to assign a global role to a user or group. It returns the JSON representation of the global role binding to be created without actually creating it. Only used for displaying the resource when using human validation.`},
			t.createGlobalRoleBindingPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteGlobalRoleBinding",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Removes a global role from a user or group by deleting the global role binding (GRB) with the given name.
			Use listGlobalRoleBindings to find the name of the binding to delete.`},
			t.deleteGlobalRoleBinding)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteGlobalRoleBindingPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to remove a global role from a user or group. It returns the JSON representation of the global role binding to be deleted without actually deleting it. Only used for displaying the resource when using human validation.`},
			t.deleteGlobalRoleBindingPlan)
	}
}
