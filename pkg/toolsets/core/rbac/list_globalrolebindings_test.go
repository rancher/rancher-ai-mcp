package rbac

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
)

func listGlobalRoleBindingsObjects() []runtime.Object {
	return append([]runtime.Object{
		globalRoleBinding("grb-1", "admin", "userName", "u-user1"),
		globalRoleBinding("grb-2", "user", "userName", "u-user2"),
		globalRoleBinding("grb-3", "user", "groupPrincipalName", "github_team://1234"),
	}, testUsers()...)
}

func TestListGlobalRoleBindingsResponse(t *testing.T) {
	tools, _ := newTestTools(t, listGlobalRoleBindingsObjects()...)

	result, _, err := tools.listGlobalRoleBindings(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), listGRBParams{GlobalRole: "admin"})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"llm": [
			{"apiVersion": "management.cattle.io/v3", "kind": "GlobalRoleBinding", "metadata": {"name": "grb-1"}, "globalRoleName": "admin", "userName": "u-user1"}
		],
		"uiContext": [
			{"cluster": "local", "kind": "GlobalRoleBinding", "name": "grb-1", "namespace": "", "type": "globalrolebinding"}
		]
	}`, result.Content[0].(*mcp.TextContent).Text)
}

func TestListGlobalRoleBindings(t *testing.T) {
	groupsNote := "includes bindings for the groups the user belongs to: github_team://1234"

	tests := map[string]struct {
		params        listGRBParams
		objects       []runtime.Object
		expected      resultSummary
		expectedError string
	}{
		"list all GRBs": {
			params:   listGRBParams{},
			expected: resultSummary{Names: []string{"grb-1", "grb-2", "grb-3"}},
		},
		"filter by user includes bindings for the user's groups": {
			params:   listGRBParams{User: "u-user1"},
			expected: resultSummary{Names: []string{"grb-1", "grb-3"}, Note: groupsNote},
		},
		"filter by username for a user without groups": {
			params:   listGRBParams{User: "user2"},
			expected: resultSummary{Names: []string{"grb-2"}},
		},
		"filter by user and global role": {
			params:   listGRBParams{User: "user1", GlobalRole: "user"},
			expected: resultSummary{Names: []string{"grb-3"}, Note: groupsNote},
		},
		"filter by group": {
			params:   listGRBParams{Group: "github_team://1234"},
			expected: resultSummary{Names: []string{"grb-3"}},
		},
		"filter by global role": {
			params:   listGRBParams{GlobalRole: "user"},
			expected: resultSummary{Names: []string{"grb-2", "grb-3"}},
		},
		"user and group cannot both be specified": {
			params:        listGRBParams{User: "u-user1", Group: "github_team://1234"},
			expectedError: "only one of user or group can be specified",
		},
		"unknown user": {
			params:        listGRBParams{User: "nobody"},
			expectedError: `user "nobody" not found`,
		},
		"no GRBs found": {
			params:   listGRBParams{},
			objects:  []runtime.Object{},
			expected: resultSummary{Note: "no resources found"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			objects := tt.objects
			if objects == nil {
				objects = listGlobalRoleBindingsObjects()
			}
			tools, _ := newTestTools(t, objects...)

			result, _, err := tools.listGlobalRoleBindings(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), tt.params)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, summarizeResult(t, result))
		})
	}
}
