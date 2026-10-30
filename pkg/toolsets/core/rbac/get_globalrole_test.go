package rbac

import (
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func TestGetGlobalRole(t *testing.T) {
	admin := globalRole("admin", map[string]any{
		"displayName": "Admin",
		"rules": []any{
			map[string]any{"verbs": []any{"*"}, "resources": []any{"*"}, "apiGroups": []any{"*"}},
		},
	})
	nodeDrivers := globalRole("nodedrivers-manage", map[string]any{"displayName": "Manage Node Drivers"})
	duplicate1 := globalRole("gr-aaaaa", map[string]any{"displayName": "Test Role"})
	duplicate2 := globalRole("gr-bbbbb", map[string]any{"displayName": "Test Role"})
	noDisplayName := globalRole("gr-nodisplay", nil)

	tests := map[string]struct {
		params         getGlobalRoleParams
		objects        []runtime.Object
		reactorErr     error
		expected       resultSummary
		expectedResult string
		expectedError  string
	}{
		"get global role by name": {
			params:  getGlobalRoleParams{Name: "admin"},
			objects: []runtime.Object{admin, nodeDrivers},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "GlobalRole",
						"metadata": {"name": "admin"},
						"displayName": "Admin",
						"rules": [{"verbs": ["*"], "resources": ["*"], "apiGroups": ["*"]}]
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "GlobalRole", "name": "admin", "namespace": "", "type": "globalrole"}
				]
			}`,
		},
		"get global role by display name ignoring case": {
			params:   getGlobalRoleParams{Name: "manage node drivers"},
			objects:  []runtime.Object{admin, nodeDrivers},
			expected: resultSummary{Names: []string{"nodedrivers-manage"}},
		},
		"multiple global roles with the display name": {
			params:   getGlobalRoleParams{Name: "Test Role"},
			objects:  []runtime.Object{admin, duplicate1, duplicate2},
			expected: resultSummary{Names: []string{"gr-aaaaa", "gr-bbbbb"}, Note: "multiple global roles have this display name, ask the user which one they mean"},
		},
		"no match returns the available global roles": {
			params:  getGlobalRoleParams{Name: "Configure Node Drivers"},
			objects: []runtime.Object{admin, nodeDrivers, noDisplayName},
			expectedResult: `{
				"llm": {
					"note": "no global role has the name or display name \"Configure Node Drivers\". Suggest the closest of the available global roles to the user",
					"availableGlobalRoles": [
						{"name": "admin", "displayName": "Admin"},
						{"name": "gr-nodisplay"},
						{"name": "nodedrivers-manage", "displayName": "Manage Node Drivers"}
					]
				}
			}`,
		},
		"errors other than not found are returned": {
			params:        getGlobalRoleParams{Name: "admin"},
			objects:       []runtime.Object{admin},
			reactorErr:    errors.New("connection refused"),
			expectedError: "connection refused",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeDynClient := newTestTools(t, tt.objects...)
			if tt.reactorErr != nil {
				fakeDynClient.PrependReactor("get", "globalroles", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, tt.reactorErr
				})
			}

			result, _, err := tools.getGlobalRole(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), tt.params)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			if tt.expectedResult != "" {
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
				return
			}
			assert.Equal(t, tt.expected, summarizeResult(t, result))
		})
	}
}
