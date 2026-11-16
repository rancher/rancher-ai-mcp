package rbac

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
)

const (
	fakeURL   = "https://localhost:8080"
	fakeToken = "fakeToken"
)

var rbacGVRs = map[schema.GroupVersionResource]string{
	{Group: "management.cattle.io", Version: "v3", Resource: "clusterroletemplatebindings"}: "ClusterRoleTemplateBindingList",
	{Group: "management.cattle.io", Version: "v3", Resource: "globalroles"}:                 "GlobalRoleList",
	{Group: "management.cattle.io", Version: "v3", Resource: "globalrolebindings"}:          "GlobalRoleBindingList",
	{Group: "management.cattle.io", Version: "v3", Resource: "projects"}:                    "ProjectList",
	{Group: "management.cattle.io", Version: "v3", Resource: "projectroletemplatebindings"}: "ProjectRoleTemplateBindingList",
	{Group: "management.cattle.io", Version: "v3", Resource: "roletemplates"}:               "RoleTemplateList",
	{Group: "management.cattle.io", Version: "v3", Resource: "users"}:                       "UserList",
	{Group: "management.cattle.io", Version: "v3", Resource: "userattributes"}:              "UserAttributeList",
	{Group: "management.cattle.io", Version: "v3", Resource: "tokens"}:                      "TokenList",
	{Group: "ext.cattle.io", Version: "v1", Resource: "tokens"}:                             "TokenList",
}

// testUsers returns users u-user1, u-user2 and u-user3, where u-user1 belongs to the group
// github_team://1234 and u-user3 belongs to the local group local://g-abc.
func testUsers() []runtime.Object {
	return []runtime.Object{
		user("u-user1", "user1", map[string]any{"principalIds": []any{"github_user://99", "local://u-user1"}}),
		user("u-user2", "user2", nil),
		user("u-user3", "user3", nil),
		managementObject("UserAttribute", "u-user3", "", map[string]any{
			"GroupPrincipals": map[string]any{
				"local": map[string]any{
					"Items": []any{
						map[string]any{"metadata": map[string]any{"name": "local://g-abc"}, "displayName": "local group"},
					},
				},
			},
		}),
		managementObject("UserAttribute", "u-user1", "", map[string]any{
			"GroupPrincipals": map[string]any{
				"github": map[string]any{
					"Items": []any{
						map[string]any{"metadata": map[string]any{"name": "github_team://1234"}, "displayName": "team"},
					},
				},
			},
		}),
	}
}

func rbacScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = metav1.AddMetaToScheme(scheme)

	return scheme
}

// startMCPServer connects a client to an MCP server with tools registered, over an in-memory transport.
// Both sessions are closed when the test ends.
func startMCPServer(t *testing.T, tools *Tools) *mcp.ClientSession {
	t.Helper()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1.0.0"}, nil)
	tools.AddTools(mcpServer)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	ss, err := mcpServer.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

func TestAddTools(t *testing.T) {
	c, err := client.NewClient(true, fakeURL)
	require.NoError(t, err)

	cs := startMCPServer(t, NewTools(c, false))

	toolsResult, err := cs.ListTools(t.Context(), &mcp.ListToolsParams{})
	assert.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 23, "incorrect number of RBAC tools registered")

	for _, tool := range toolsResult.Tools {
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
	}
}

func TestAddToolsReadOnly(t *testing.T) {
	c, err := client.NewClient(true, fakeURL)
	require.NoError(t, err)

	cs := startMCPServer(t, NewTools(c, true))

	toolsResult, err := cs.ListTools(t.Context(), &mcp.ListToolsParams{})
	assert.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 13, "incorrect number of RBAC tools registered in read-only mode")

	for _, tool := range toolsResult.Tools {
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
	}
}

// TestUserParamDescriptions checks that every tool with a user parameter documents that it accepts a
// username or user ID, since MCP clients use the schema description to construct arguments.
func TestUserParamDescriptions(t *testing.T) {
	c, err := client.NewClient(true, fakeURL)
	require.NoError(t, err)

	cs := startMCPServer(t, NewTools(c, false))

	toolsResult, err := cs.ListTools(t.Context(), &mcp.ListToolsParams{})
	require.NoError(t, err)

	var checked []string

	for _, tool := range toolsResult.Tools {
		schemaBytes, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)

		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(schemaBytes, &schema))

		if userParam, ok := schema.Properties["user"]; ok {
			assert.Contains(t, userParam.Description, "username or user ID", "user parameter of %s", tool.Name)
			checked = append(checked, tool.Name)
		}
	}

	assert.ElementsMatch(t, []string{
		"createGlobalRoleBinding", "createGlobalRoleBindingPlan",
		"createProjectRoleTemplateBinding", "createProjectRoleTemplateBindingPlan",
		"listClusterRoleTemplateBindings", "listGlobalRoleBindings", "listProjectRoleTemplateBindings",
		"listTokenUsage",
	}, checked)
}

// TestUserToolAnnotations checks that the user and authentication tools tell clients they only read, so clients
// can run them without asking for confirmation.
func TestUserToolAnnotations(t *testing.T) {
	c, err := client.NewClient(true, fakeURL)
	require.NoError(t, err)

	cs := startMCPServer(t, NewTools(c, true))

	toolsResult, err := cs.ListTools(t.Context(), &mcp.ListToolsParams{})
	require.NoError(t, err)

	annotated := map[string]*mcp.ToolAnnotations{}
	for _, tool := range toolsResult.Tools {
		annotated[tool.Name] = tool.Annotations
	}

	for name, title := range map[string]string{
		"getUser":            "Get user",
		"getCurrentUser":     "Get current user",
		"listAuthProviders":  "List authentication providers",
		"listUsers":          "List users",
		"auditInactiveUsers": "Audit inactive users",
		"listTokenUsage":     "List token usage",
	} {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, annotated, name, "tool should be registered in read-only mode")
			assert.Equal(t, &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}, annotated[name])
		})
	}
}
