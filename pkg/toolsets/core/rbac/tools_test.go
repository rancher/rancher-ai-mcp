package rbac

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
}

// testUsers returns users u-user1 and u-user2, where u-user1 belongs to the group github_team://1234.
func testUsers() []runtime.Object {
	return []runtime.Object{
		user("u-user1", "user1", map[string]any{"principalIds": []any{"github_user://99", "local://u-user1"}}),
		user("u-user2", "user2", nil),
		managementObject("UserAttribute", "u-user1", "", map[string]any{
			"groupPrincipals": map[string]any{
				"github": map[string]any{
					"items": []any{
						map[string]any{"metadata": map[string]any{"name": "github_team://1234"}, "displayName": "team"},
					},
				},
			},
		}),
	}
}

// newTestTools returns Tools backed by a fake dynamic client containing objs, and the fake client
// so tests can inspect or modify its state.
func newTestTools(t *testing.T, objs ...runtime.Object) (*Tools, *dynamicfake.FakeDynamicClient) {
	t.Helper()

	fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(rbacScheme(), rbacGVRs, objs...)
	c := &client.Client{
		DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) { return fakeDynClient, nil },
	}

	return NewTools(test.WrapClient(c, fakeToken), false), fakeDynClient
}

func rbacScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = metav1.AddMetaToScheme(scheme)
	return scheme
}

func startMCPServer(t *testing.T, tools *Tools) (*mcp.ClientSession, func()) {
	t.Helper()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1.0.0"}, nil)
	tools.AddTools(mcpServer)

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{Handler: handler}
	go server.Serve(listener)

	var cs *mcp.ClientSession
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: "http://" + listener.Addr().String()}

	assert.Eventually(t, func() bool {
		cs, err = mcpClient.Connect(context.Background(), transport, nil)
		return err == nil
	}, 2*time.Second, 100*time.Millisecond)

	require.NotNil(t, cs)
	return cs, func() {
		cs.Close()
		server.Shutdown(context.Background())
		listener.Close()
	}
}

func TestAddTools(t *testing.T) {
	c, err := client.NewClient(true, fakeURL)
	require.NoError(t, err)
	cs, cleanup := startMCPServer(t, NewTools(c, false))
	defer cleanup()

	toolsResult, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	assert.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 16, "incorrect number of RBAC tools registered")
	for _, tool := range toolsResult.Tools {
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
	}
}

func TestAddToolsReadOnly(t *testing.T) {
	c, err := client.NewClient(true, fakeURL)
	require.NoError(t, err)
	cs, cleanup := startMCPServer(t, NewTools(c, true))
	defer cleanup()

	toolsResult, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	assert.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 8, "incorrect number of RBAC tools registered in read-only mode")
	for _, tool := range toolsResult.Tools {
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
	}
}

// TestUserParamDescriptions checks that every tool with a user parameter documents that it accepts a
// username or user ID, since MCP clients use the schema description to construct arguments.
func TestUserParamDescriptions(t *testing.T) {
	c, err := client.NewClient(true, fakeURL)
	require.NoError(t, err)
	cs, cleanup := startMCPServer(t, NewTools(c, false))
	defer cleanup()

	toolsResult, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
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
		"listClusterRoleTemplateBindings", "listGlobalRoleBindings", "listProjectRoleTemplateBindings",
	}, checked)
}
