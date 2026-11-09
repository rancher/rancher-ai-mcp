package rbac

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var globalRolesGVR = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "globalroles"}

// deleteGlobalRoleObjects returns the custom global role gr-test assigned to u-user1, the custom global
// role gr-shared assigned to u-user1 and a group, and the built-in global role admin assigned to u-user2.
// The resources for gr-test have server-managed fields that should be removed from responses.
func deleteGlobalRoleObjects() []runtime.Object {
	return []runtime.Object{
		withServerFields(globalRole("gr-test", map[string]any{"displayName": "Test Role"})),
		globalRole("gr-shared", map[string]any{"displayName": "Shared Role"}),
		globalRole("admin", map[string]any{"displayName": "Admin", "builtin": true}),
		withServerFields(globalRoleBinding("grb-1", "gr-test", "userName", "u-user1")),
		globalRoleBinding("grb-2", "admin", "userName", "u-user2"),
		globalRoleBinding("grb-3", "gr-shared", "userName", "u-user1"),
		globalRoleBinding("grb-4", "gr-shared", "groupPrincipalName", "github_team://1234"),
	}
}

func TestDeleteGlobalRole(t *testing.T) {
	tests := map[string]struct {
		params         deleteGlobalRoleParams
		expectedResult string
		expectedError  string
		expectedRoles  []string
	}{
		"delete global role with a binding": {
			params: deleteGlobalRoleParams{Name: "gr-test"},
			expectedResult: `{
				"llm": {
					"globalRole": {
						"apiVersion": "management.cattle.io/v3",
						"kind": "GlobalRole",
						"metadata": {"name": "gr-test", "annotations": {"field.cattle.io/creatorId": "user-sdxtj"}},
						"displayName": "Test Role"
					},
					"globalRoleBindings": [{"name": "grb-1", "userName": "u-user1"}],
					"note": "the global role has been deleted, along with the global role binding that assigned it to a user or group"
				}
			}`,
			expectedRoles: []string{"admin", "gr-shared"},
		},
		"delete global role with bindings": {
			params: deleteGlobalRoleParams{Name: "gr-shared"},
			expectedResult: `{
				"llm": {
					"globalRole": {"apiVersion": "management.cattle.io/v3", "kind": "GlobalRole", "metadata": {"name": "gr-shared"}, "displayName": "Shared Role"},
					"globalRoleBindings": [
						{"name": "grb-3", "userName": "u-user1"},
						{"name": "grb-4", "groupPrincipalName": "github_team://1234"}
					],
					"note": "the global role has been deleted, along with 2 global role bindings that assigned it to users or groups"
				}
			}`,
			expectedRoles: []string{"admin", "gr-test"},
		},
		"built-in global role cannot be deleted": {
			params:        deleteGlobalRoleParams{Name: "admin"},
			expectedError: `global role "admin" is built-in and cannot be deleted`,
		},
		"global role not found": {
			params:        deleteGlobalRoleParams{Name: "nonexistent"},
			expectedError: `global role "nonexistent" not found, use getGlobalRole to find the name from a display name`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeDynClient := newTestTools(t, deleteGlobalRoleObjects()...)

			result, _, err := tools.deleteGlobalRole(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)

			remaining, listErr := fakeDynClient.Resource(globalRolesGVR).List(t.Context(), metav1.ListOptions{})
			require.NoError(t, listErr)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Len(t, remaining.Items, 3, "no global role should be deleted")

				return
			}

			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)

			var names []string
			for _, item := range remaining.Items {
				names = append(names, item.GetName())
			}

			assert.ElementsMatch(t, tt.expectedRoles, names)
		})
	}
}

func TestDeleteGlobalRoleWithoutBindings(t *testing.T) {
	tools, _ := newTestTools(t, globalRole("gr-unused", nil))

	result, _, err := tools.deleteGlobalRole(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), deleteGlobalRoleParams{Name: "gr-unused"})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"llm": {
			"globalRole": {"apiVersion": "management.cattle.io/v3", "kind": "GlobalRole", "metadata": {"name": "gr-unused"}},
			"note": "the global role has been deleted"
		}
	}`, result.Content[0].(*mcp.TextContent).Text)
}

func TestDeleteGlobalRolePlan(t *testing.T) {
	tools, fakeDynClient := newTestTools(t, deleteGlobalRoleObjects()...)
	ctx := middleware.WithToken(t.Context(), fakeToken)

	result, _, err := tools.deleteGlobalRolePlan(ctx, test.NewCallToolRequest(fakeURL), deleteGlobalRoleParams{Name: "gr-test"})
	require.NoError(t, err)

	var planResources []response.PlanResource
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &planResources))
	require.Len(t, planResources, 2)
	assert.Equal(t, response.OperationDelete, planResources[0].Type)
	assert.Equal(t, "GlobalRole", planResources[0].Resource.Kind)
	assert.Equal(t, "gr-test", planResources[0].Resource.Name)
	assert.NotContains(t, planResources[0].Payload, "status")
	assert.NotContains(t, planResources[0].Payload.(map[string]any)["metadata"], "managedFields")
	assert.NotContains(t, planResources[0].Payload.(map[string]any)["metadata"], "finalizers")
	assert.Equal(t, response.OperationDelete, planResources[1].Type)
	assert.Equal(t, "GlobalRoleBinding", planResources[1].Resource.Kind)
	assert.Equal(t, "grb-1", planResources[1].Resource.Name)

	// The plan must not delete the global role.
	remaining, err := fakeDynClient.Resource(globalRolesGVR).List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, remaining.Items, 3)

	_, _, err = tools.deleteGlobalRolePlan(ctx, test.NewCallToolRequest(fakeURL), deleteGlobalRoleParams{Name: "admin"})
	assert.ErrorContains(t, err, "is built-in and cannot be deleted")
}
