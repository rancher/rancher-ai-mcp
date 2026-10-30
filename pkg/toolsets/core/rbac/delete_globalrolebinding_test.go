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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// newTestGRB returns a binding of authn-manage to u-user1, with server-managed fields that
// should be removed from responses.
func newTestGRB() *unstructured.Unstructured {
	grb := globalRoleBinding("grb-1", "authn-manage", "userName", "u-user1")
	grb.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "rancher", Operation: metav1.ManagedFieldsOperationUpdate}})
	grb.Object["status"] = map[string]any{"summary": "Completed"}
	return grb
}

func TestDeleteGlobalRoleBinding(t *testing.T) {
	tests := map[string]struct {
		params         deleteGRBParams
		expectedResult string
		expectedError  string
	}{
		"delete global role binding": {
			params: deleteGRBParams{Name: "grb-1"},
			expectedResult: `{
				"llm": {
					"resources": [
						{"apiVersion": "management.cattle.io/v3", "kind": "GlobalRoleBinding", "metadata": {"name": "grb-1"}, "globalRoleName": "authn-manage", "userName": "u-user1"}
					],
					"note": "the global role binding has been deleted"
				}
			}`,
		},
		"global role binding not found": {
			params:        deleteGRBParams{Name: "nonexistent"},
			expectedError: "not found",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeDynClient := newTestTools(t, newTestGRB())

			result, _, err := tools.deleteGlobalRoleBinding(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)

			remaining, err := fakeDynClient.Resource(schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "globalrolebindings"}).List(t.Context(), metav1.ListOptions{})
			require.NoError(t, err)
			assert.Empty(t, remaining.Items)
		})
	}
}

func TestDeleteGlobalRoleBindingPlan(t *testing.T) {
	tools, fakeDynClient := newTestTools(t, newTestGRB())
	ctx := middleware.WithToken(t.Context(), fakeToken)

	result, _, err := tools.deleteGlobalRoleBindingPlan(ctx, test.NewCallToolRequest(fakeURL), deleteGRBParams{Name: "grb-1"})
	require.NoError(t, err)

	var planResources []response.PlanResource
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &planResources))
	require.Len(t, planResources, 1)
	assert.Equal(t, response.OperationDelete, planResources[0].Type)
	assert.Equal(t, "grb-1", planResources[0].Resource.Name)
	assert.Equal(t, "GlobalRoleBinding", planResources[0].Resource.Kind)
	payload := planResources[0].Payload.(map[string]any)
	assert.Equal(t, "authn-manage", payload["globalRoleName"])
	assert.NotContains(t, payload, "status")
	assert.NotContains(t, payload["metadata"], "managedFields")

	// The plan must not delete the binding.
	remaining, err := fakeDynClient.Resource(schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "globalrolebindings"}).List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, remaining.Items, 1)

	_, _, err = tools.deleteGlobalRoleBindingPlan(ctx, test.NewCallToolRequest(fakeURL), deleteGRBParams{Name: "nonexistent"})
	assert.ErrorContains(t, err, "not found")
}
