// Package rbac provides the MCP tools for Rancher users, authentication, tokens and role-based access control.
package rbac

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
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
	GetRancherURL(ctx context.Context, token string, method string, rancherPath string, body any, v any) error
}

// readOnlyAnnotations returns the annotations of a tool that only reads from Rancher. The tools only talk to
// the Rancher server they are configured for, so they do not interact with an open world.
func readOnlyAnnotations(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:          title,
		ReadOnlyHint:   true,
		IdempotentHint: true,
		OpenWorldHint:  new(false),
	}
}

// Tools contains tools for interacting with RBAC in Rancher.
type Tools struct {
	client   toolsClient
	ReadOnly bool
	// now returns the current time. Tests replace it to control dates such as how long ago a user logged in.
	now func() time.Time
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, readOnly bool) *Tools {
	return &Tools{
		client:   client,
		ReadOnly: readOnly,
		now:      time.Now,
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
		CRTBs provide users permissions as specified by a RoleTemplate at the cluster level.`,
	},
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
		PRTBs provide users permissions as specified by a RoleTemplate in a project.`,
	},
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
		Role templates define a set of permissions that can be assigned to users or groups.`,
	},
		t.listRoleTemplates,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "getUser",
		Annotations: readOnlyAnnotations("Get user"),
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Get a user by user ID, login name, display name, principal ID, or the username they log in with at an external provider,
		e.g. "u-abc12", "admin", "Jo Smith", "openldap_user://uid=jo,dc=example,dc=org" or "jo@example.com".
		Display names and provider usernames are matched case-insensitively. Users from external providers often have no login name,
		so find them by provider username (such as the email address they log in to an OIDC provider with), display name or principal ID.
		Every matching user is returned. If more than one matches, ask which one is meant.
		Returns the user ID, login name, display name, the authentication providers the user can log in with (authProviders),
		whether the account is enabled, when it was created, when the user last logged in, the global roles bound directly to the user,
		the groups the user belongs to (groups), the global roles they have through those groups (groupGlobalRoles),
		and the username they log in with at each external provider (providerUsernames).
		authProviders includes local only for users with a login name, since users created by an external provider have no local password.
		It does not check whether each provider is enabled, use listAuthProviders for that. System accounts have no authProviders.
		groups and providerUsernames are as Rancher recorded them when the user last logged in, or last refreshed their groups.
		Report both directGlobalRoles and groupGlobalRoles when describing what a user can do: administrators often get admin through a group.
		lastLogin.state is one of recorded, noLoginRecorded, unknown, or notApplicable for system accounts.
		Using an API token does not count as a login, so a user who only uses API tokens can look inactive.
		unavailableFields lists information the credentials are not allowed to read.`,
	},
		t.getUser,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "getCurrentUser",
		Annotations: readOnlyAnnotations("Get current user"),
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Description: `Get the Rancher user that owns the credentials used to call Rancher. Use this to answer "who am I?".
		Returns the user ID, login name, the principal IDs Rancher reports for the token and their providers (principalProviders, e.g. local, openldap, github), and group principals.
		The login name and principals come from Rancher's view of the token, so they can differ from the username on the User resource and from how the token was issued.
		To find every provider the user can log in with, use getUser with the returned user ID and read its authProviders.
		getUser can also find the user by the returned login name, even when it is a provider username such as an email address.
		When the MCP server is configured with a fixed token this is the owner of that token, which may not be the person asking.
		Use getUser with the returned user ID for more details about the user.`,
	},
		t.getCurrentUser,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "listAuthProviders",
		Annotations: readOnlyAnnotations("List authentication providers"),
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Description: `List the authentication providers enabled in Rancher, e.g. local, keycloakoidc, openldap, activedirectory or github.
		Use this to answer which authentication mechanisms are active, or how users can log in.
		Each provider's name is also the prefix of its users' principal IDs, e.g. keycloakoidc_user://.
		Works for every user, without needing permission to read Rancher's auth configuration.`,
	},
		t.listAuthProviders,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "listUsers",
		Annotations: readOnlyAnnotations("List users"),
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List Rancher users, with the same summary of each user as getUser, except that the groups each user belongs to are left out
		to keep the list short. groupGlobalRoles is still included, so users who are administrators through a group stand out; use getUser for a user's groups.
		System accounts that Rancher creates for itself are left out and counted in systemAccountsExcluded, unless includeSystemAccounts is set.
		total is the number of users across all pages. Results are paginated with limit and offset.
		If visibility is limited, the credentials are not allowed to list users and only the caller's own user is included, so do not
		conclude that no other users exist.`,
	},
		t.listUsers,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "auditInactiveUsers",
		Annotations: readOnlyAnnotations("Audit inactive users"),
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Audit Rancher users and report those who have not logged in or used a token for at least inactiveDays days, e.g. 90 for 3 months.
		A user is active if they have logged in or used a token (an API key, kubeconfig or session) since the cutoff; activeByTokenUse counts users who are only active through tokens.
		A user is inactive if their last login is before the cutoff, or if they have never logged in and their account was created before the cutoff, and they have not used a token since.
		Users created after the cutoff who have never logged in are not inactive, and are counted in recentlyCreatedNoLogin.
		System accounts are not audited. Users whose last login cannot be read are listed in undeterminedUsers.
		Inactive users are sorted by how long they have been inactive, longest first, and include their direct and group global roles, so inactive administrators stand out.
		activeByTokenUseUsers lists the users who have not logged in since the cutoff but have used a token since, with their last login and last token use.
		When asked who has not logged in, as opposed to who is inactive, report inactiveUsers and activeByTokenUseUsers, saying that the latter still use tokens.
		Counts cover every user checked, not only the returned page. Already-disabled accounts are included and counted in alreadyDisabled.
		Always pass on the caveat in notes: deleted tokens leave no record, or, if includeTokenActivity is false, token use is not counted at all.
		If visibility is limited, only the caller's own user or tokens could be read, so do not conclude that no users are inactive.`,
	},
		t.auditInactiveUsers,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "listTokenUsage",
		Annotations: readOnlyAnnotations("List token usage"),
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Report the tokens each user has and when each was last used, from both the v3 tokens and the newer ext.cattle.io tokens.
		Use this to find out whether a user or automation account is active through API keys or kubeconfigs, since using a token does not count as a login.
		Each token has a type (session for logins, apiKey for API keys, or another kind such as kubeconfig), when it expires, whether it has expired, and when it was last used.
		Users are sorted by their most recent token use. Expired tokens are counted but only listed if includeExpired is set. Token values are never returned.
		If visibility is limited, only the caller's own tokens are included, so do not conclude that other users have no tokens.`,
	},
		t.listTokenUsage,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getRoleTemplate",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Get a role template by name.`,
	},
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
		Global roles define permissions that apply across Rancher rather than to a specific cluster or project.`,
	},
		t.listGlobalRoles,
	)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getGlobalRole",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Get a global role by name or display name, including the rules and inherited cluster roles that define its permissions.
		Names and display names are matched case-insensitively. If more than one global role matches, all of them are returned.
		If no global role matches, the names and display names of all global roles are returned so the closest one can be suggested.`,
	},
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
		To find out everything a user can do, combine GRBs with their cluster and project role template bindings.`,
	},
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
			Use createGlobalRoleBinding to assign the global role to users or groups.`,
		},
			t.createGlobalRole)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createGlobalRolePlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			InputSchema: createGlobalRoleInputSchema(),
			Description: `Plans to create a global role in Rancher. It returns the JSON representation of the global role to be created without actually creating it. Only used for displaying the resource when using human validation.`,
		},
			t.createGlobalRolePlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteGlobalRole",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Deletes a global role by name. Built-in global roles cannot be deleted.
			Rancher also deletes every global role binding (GRB) for the global role, so all users and groups assigned the role lose it.
			Use getGlobalRole to find the name from a display name.`,
		},
			t.deleteGlobalRole)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteGlobalRolePlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to delete a global role. It returns the JSON representation of the global role to be deleted without actually deleting it, with a caution giving the number of users and groups bound to the global role. Only used for displaying the resource when using human validation.`,
		},
			t.deleteGlobalRolePlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createGlobalRoleBinding",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Assigns a global role to a user or group by creating a global role binding (GRB).
			Exactly one of user or group must be specified. The user can be a username or user ID.
			The global role must be specified by name. Use getGlobalRole to find the name from a display name.`,
		},
			t.createGlobalRoleBinding)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createGlobalRoleBindingPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to assign a global role to a user or group. It returns the JSON representation of the global role binding to be created without actually creating it. Only used for displaying the resource when using human validation.`,
		},
			t.createGlobalRoleBindingPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createProjectRoleTemplateBinding",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:    false,
				DestructiveHint: new(false),
				IdempotentHint:  false,
			},
			Description: `Grants a user or group a role in a project by creating a project role template binding (PRTB), e.g. to add a project member.
		Common project roles are project-owner, project-member and read-only. Defaults to project-member.
		If the user or group already has the role in the project, no changes are made.`,
		},
			t.createProjectRoleTemplateBinding,
		)
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createProjectRoleTemplateBindingPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:    true,
				DestructiveHint: new(false),
				IdempotentHint:  true,
			},
			Description: `Plans to grant a user or group a role in a project. It returns the JSON representation of the project role template binding (PRTB) to be created without actually creating it in the cluster. Only used for displaying the resource when using human validation.`,
		},
			t.createProjectRoleTemplateBindingPlan,
		)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteGlobalRoleBinding",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Removes a global role from a user or group by deleting the global role binding (GRB) with the given name.
			Use listGlobalRoleBindings to find the name of the binding to delete.`,
		},
			t.deleteGlobalRoleBinding)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "deleteGlobalRoleBindingPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to remove a global role from a user or group. It returns the JSON representation of the global role binding to be deleted without actually deleting it. Only used for displaying the resource when using human validation.`,
		},
			t.deleteGlobalRoleBindingPlan)
	}
}
