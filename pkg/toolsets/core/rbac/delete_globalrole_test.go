package rbac

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var globalRolesGVR = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "globalroles"}

// deleteGlobalRoleObjects returns the custom global role gr-test assigned to u-user1, with server-managed
// fields that should be removed from responses, and the built-in global role admin assigned to u-user2.
func deleteGlobalRoleObjects() []runtime.Object {
	testRole := globalRole("gr-test", map[string]any{
		"displayName": "Test Role",
		"status":      map[string]any{"summary": "Completed"},
	})
	testRole.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "rancher", Operation: metav1.ManagedFieldsOperationUpdate}})
	testRoleBinding := globalRoleBinding("grb-1", "gr-test", "userName", "u-user1")
	testRoleBinding.Object["status"] = map[string]any{"summary": "Completed"}

	return []runtime.Object{
		testRole,
		globalRole("admin", map[string]any{"displayName": "Admin", "builtin": true}),
		testRoleBinding,
		globalRoleBinding("grb-2", "admin", "userName", "u-user2"),
	}
}

func TestDeleteGlobalRole(t *testing.T) {
	tests := map[string]struct {
		params         deleteGlobalRoleParams
		expectedResult string
		expectedError  string
	}{
		"delete global role with bindings": {
			params: deleteGlobalRoleParams{Name: "gr-test"},
			expectedResult: `{
				"llm": {
					"resources": [
						{"apiVersion": "management.cattle.io/v3", "kind": "GlobalRole", "metadata": {"name": "gr-test"}, "displayName": "Test Role"},
						{"apiVersion": "management.cattle.io/v3", "kind": "GlobalRoleBinding", "metadata": {"name": "grb-1"}, "globalRoleName": "gr-test", "userName": "u-user1"}
					],
					"note": "the global role has been deleted, along with 1 global role binding(s) that assigned it to users or groups"
				}
			}`,
		},
		"built-in global role cannot be deleted": {
			params:        deleteGlobalRoleParams{Name: "admin"},
			expectedError: `global role "admin" is built-in and cannot be deleted`,
		},
		"global role not found": {
			params:        deleteGlobalRoleParams{Name: "nonexistent"},
			expectedError: "not found",
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
				assert.Len(t, remaining.Items, 2, "no global role should be deleted")
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			require.Len(t, remaining.Items, 1)
			assert.Equal(t, "admin", remaining.Items[0].GetName())
		})
	}
}

func TestDeleteGlobalRoleWithoutBindings(t *testing.T) {
	tools, _ := newTestTools(t, globalRole("gr-unused", nil))

	result, _, err := tools.deleteGlobalRole(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), deleteGlobalRoleParams{Name: "gr-unused"})
	require.NoError(t, err)
	assert.Equal(t, resultSummary{Names: []string{"gr-unused"}, Note: "the global role has been deleted"}, summarizeResult(t, result))
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
	assert.Equal(t, response.OperationDelete, planResources[1].Type)
	assert.Equal(t, "GlobalRoleBinding", planResources[1].Resource.Kind)
	assert.Equal(t, "grb-1", planResources[1].Resource.Name)

	// The plan must not delete the global role.
	remaining, err := fakeDynClient.Resource(globalRolesGVR).List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, remaining.Items, 2)

	_, _, err = tools.deleteGlobalRolePlan(ctx, test.NewCallToolRequest(fakeURL), deleteGlobalRoleParams{Name: "admin"})
	assert.ErrorContains(t, err, "is built-in and cannot be deleted")
}
