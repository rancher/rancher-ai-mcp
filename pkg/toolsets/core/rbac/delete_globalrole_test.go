package rbac

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
			tools, fakeDynClient := newFakeTools(t, withObjects(deleteGlobalRoleObjects()...))

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
	tools, _ := newFakeTools(t, withObjects(globalRole("gr-unused", nil)))

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
	tests := map[string]struct {
		name           string
		expectedDetail string
	}{
		"global role bound to a user": {
			name:           "gr-test",
			expectedDetail: "There is 1 user bound to the role(s) about to be deleted.",
		},
		"global role bound to a user and a group": {
			name:           "gr-shared",
			expectedDetail: "There is 1 user and 1 group bound to the role(s) about to be deleted.",
		},
		"global role without bindings": {
			name: "gr-unused",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeDynClient := newFakeTools(t, withObjects(append(deleteGlobalRoleObjects(), globalRole("gr-unused", nil))...))

			result, _, err := tools.deleteGlobalRolePlan(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), deleteGlobalRoleParams{Name: tt.name})
			require.NoError(t, err)

			// The plan only includes the global role, not the bindings Rancher deletes with it.
			var planResources []response.PlanResource
			require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &planResources))
			require.Len(t, planResources, 1)
			assert.Equal(t, response.OperationDelete, planResources[0].Type)
			assert.Equal(t, "GlobalRole", planResources[0].Resource.Kind)
			assert.Equal(t, tt.name, planResources[0].Resource.Name)
			assert.Equal(t, tt.expectedDetail, planResources[0].Detail)

			payload, ok := planResources[0].Payload.(map[string]any)
			require.True(t, ok, "the payload should be an object")
			assert.NotContains(t, payload, "status")
			assert.NotContains(t, payload["metadata"], "managedFields")
			assert.NotContains(t, payload["metadata"], "finalizers")

			// The plan must not delete the global role.
			remaining, err := fakeDynClient.Resource(globalRolesGVR).List(t.Context(), metav1.ListOptions{})
			require.NoError(t, err)
			assert.Len(t, remaining.Items, 4)
		})
	}
}

func TestDeleteGlobalRolePlanBuiltIn(t *testing.T) {
	tools, _ := newFakeTools(t, withObjects(deleteGlobalRoleObjects()...))

	_, _, err := tools.deleteGlobalRolePlan(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), deleteGlobalRoleParams{Name: "admin"})
	assert.ErrorContains(t, err, "is built-in and cannot be deleted")
}

func TestBoundSubjectsCaution(t *testing.T) {
	tests := map[string]struct {
		grbs     []*unstructured.Unstructured
		expected string
	}{
		"no bindings": {
			expected: "",
		},
		"one user": {
			grbs:     []*unstructured.Unstructured{globalRoleBinding("grb-1", "gr", "userName", "u-user1")},
			expected: "There is 1 user bound to the role(s) about to be deleted.",
		},
		"one group": {
			grbs:     []*unstructured.Unstructured{globalRoleBinding("grb-1", "gr", "groupPrincipalName", "github_team://1234")},
			expected: "There is 1 group bound to the role(s) about to be deleted.",
		},
		"users and groups": {
			grbs: []*unstructured.Unstructured{
				globalRoleBinding("grb-1", "gr", "userName", "u-user1"),
				globalRoleBinding("grb-2", "gr", "userName", "u-user2"),
				globalRoleBinding("grb-3", "gr", "groupPrincipalName", "github_team://1234"),
				globalRoleBinding("grb-4", "gr", "groupPrincipalName", "github_team://5678"),
			},
			expected: "There are 2 users and 2 groups bound to the role(s) about to be deleted.",
		},
		"one user and several groups": {
			grbs: []*unstructured.Unstructured{
				globalRoleBinding("grb-1", "gr", "userName", "u-user1"),
				globalRoleBinding("grb-2", "gr", "groupPrincipalName", "github_team://1234"),
				globalRoleBinding("grb-3", "gr", "groupPrincipalName", "github_team://5678"),
			},
			expected: "There is 1 user and 2 groups bound to the role(s) about to be deleted.",
		},
		"several groups": {
			grbs: []*unstructured.Unstructured{
				globalRoleBinding("grb-1", "gr", "groupPrincipalName", "github_team://1234"),
				globalRoleBinding("grb-2", "gr", "groupPrincipalName", "github_team://5678"),
			},
			expected: "There are 2 groups bound to the role(s) about to be deleted.",
		},
		"a user with more than one binding is counted once": {
			grbs: []*unstructured.Unstructured{
				globalRoleBinding("grb-1", "gr", "userName", "u-user1"),
				globalRoleBinding("grb-2", "gr", "userName", "u-user1"),
			},
			expected: "There is 1 user bound to the role(s) about to be deleted.",
		},
		"a user bound by principal is counted": {
			grbs:     []*unstructured.Unstructured{globalRoleBinding("grb-1", "gr", "userPrincipalName", "github_user://99")},
			expected: "There is 1 user bound to the role(s) about to be deleted.",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expected, boundSubjectsCaution(tt.grbs))
		})
	}
}
