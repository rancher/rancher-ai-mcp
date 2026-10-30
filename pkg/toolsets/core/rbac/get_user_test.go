package rbac

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

func TestGetUser(t *testing.T) {
	user1 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "User",
			"metadata": map[string]any{
				"name": "u-abc123",
			},
			"username":    "admin",
			"displayName": "Default Admin",
		},
	}
	user2 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "User",
			"metadata": map[string]any{
				"name": "u-xyz456",
			},
			"username":    "jsmith",
			"displayName": "John Smith",
		},
	}

	tests := map[string]struct {
		params         getUserParams
		objects        []runtime.Object
		expectedResult string
	}{
		"get user by username": {
			params:  getUserParams{Username: "admin"},
			objects: []runtime.Object{user1, user2},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "User",
						"metadata": {"name": "u-abc123"},
						"username": "admin",
						"displayName": "Default Admin"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "User", "name": "u-abc123", "namespace": "", "type": "user"}
				]
			}`,
		},
		"no matching user returns no resources found": {
			params:         getUserParams{Username: "nonexistent"},
			objects:        []runtime.Object{user1, user2},
			expectedResult: `{"llm": "no resources found"}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(rbacScheme(), rbacGVRs, tt.objects...)
			c := &client.Client{
				DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) { return fakeDynClient, nil },
			}
			tools := NewTools(test.WrapClient(c, fakeToken), false)

			result, _, err := tools.getUser(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

func TestFindUser(t *testing.T) {
	// The impostor has the target user's ID as its username, and is listed first.
	target := user("u-zzz", "alice", nil)
	impostor := user("u-aaa", "u-zzz", nil)
	duplicate1 := user("u-dup1", "shared", nil)
	duplicate2 := user("u-dup2", "shared", nil)

	tests := map[string]struct {
		nameOrID      string
		expectedUser  string
		expectedError string
	}{
		"a user ID is preferred to another user's username": {
			nameOrID:     "u-zzz",
			expectedUser: "u-zzz",
		},
		"a unique username": {
			nameOrID:     "alice",
			expectedUser: "u-zzz",
		},
		"a username shared by more than one user is rejected": {
			nameOrID:      "shared",
			expectedError: `more than one user has the username "shared", specify one of the user IDs: u-dup1, u-dup2`,
		},
		"no match": {
			nameOrID: "nobody",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newTestTools(t, impostor, target, duplicate1, duplicate2)

			u, err := tools.findUser(middleware.WithToken(t.Context(), fakeToken), tt.nameOrID)
			if tt.expectedError != "" {
				assert.EqualError(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			if tt.expectedUser == "" {
				assert.Nil(t, u)
				return
			}
			require.NotNil(t, u)
			assert.Equal(t, tt.expectedUser, u.GetName())
		})
	}
}

// TestCreateGlobalRoleBindingResolvesUserIDBeforeUsername checks that a global role is never assigned to
// a user whose username matches another user's ID.
func TestCreateGlobalRoleBindingResolvesUserIDBeforeUsername(t *testing.T) {
	tools, _ := newTestTools(t, user("u-aaa", "u-zzz", nil), user("u-zzz", "alice", nil))

	grb, err := tools.resolveGlobalRoleBindingObj(middleware.WithToken(t.Context(), fakeToken), createGRBParams{GlobalRole: "admin", User: "u-zzz"})
	require.NoError(t, err)
	assert.Equal(t, "u-zzz", grb.Object["userName"])
}
