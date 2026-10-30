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

func TestListClusterRoleTemplateBindings(t *testing.T) {
	crtb1 := crtb("crtb-1", "local", "cluster-owner", "userName", "u-user1")
	crtb2 := crtb("crtb-2", "local", "cluster-member", "userName", "u-user2")
	crtb3 := crtb("crtb-3", "downstream", "cluster-member", "userName", "u-user1")
	crtbGroup := crtb("crtb-group", "local", "cluster-member", "groupPrincipalName", "github_team://1234")
	crtbLegacyGroup := crtb("crtb-legacy-group", "local", "cluster-owner", "groupName", "github_team://1234")
	crtbUserPrincipal := crtb("crtb-user-principal", "local", "cluster-member", "userPrincipalName", "github_user://99")
	groupsNote := "includes bindings for the groups the user belongs to: github_team://1234"

	tests := map[string]struct {
		params         listCRTBParams
		objects        []runtime.Object
		expected       resultSummary
		expectedResult string
		expectedError  string
	}{
		"list all CRTBs for a cluster": {
			params:  listCRTBParams{Cluster: "local"},
			objects: []runtime.Object{crtb1, crtb2, crtb3},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ClusterRoleTemplateBinding",
						"metadata": {"name": "crtb-1", "namespace": "local"},
						"clusterName": "local",
						"roleTemplateName": "cluster-owner",
						"userName": "u-user1"
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ClusterRoleTemplateBinding",
						"metadata": {"name": "crtb-2", "namespace": "local"},
						"clusterName": "local",
						"roleTemplateName": "cluster-member",
						"userName": "u-user2"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ClusterRoleTemplateBinding", "name": "crtb-1", "namespace": "local", "type": "clusterroletemplatebinding"},
					{"cluster": "local", "kind": "ClusterRoleTemplateBinding", "name": "crtb-2", "namespace": "local", "type": "clusterroletemplatebinding"}
				]
			}`,
		},
		"filter by user": {
			params:   listCRTBParams{Cluster: "local", User: "u-user1"},
			objects:  []runtime.Object{crtb1, crtb2, crtb3},
			expected: resultSummary{Names: []string{"crtb-1"}, Note: groupsNote},
		},
		"filter by username includes bindings for the user's groups": {
			params:   listCRTBParams{Cluster: "local", User: "user1"},
			objects:  []runtime.Object{crtb1, crtb2, crtbGroup},
			expected: resultSummary{Names: []string{"crtb-1", "crtb-group"}, Note: groupsNote},
		},
		"filter by user matches userPrincipalName and legacy groupName bindings": {
			params:   listCRTBParams{Cluster: "local", User: "user1"},
			objects:  []runtime.Object{crtb2, crtbLegacyGroup, crtbUserPrincipal},
			expected: resultSummary{Names: []string{"crtb-legacy-group", "crtb-user-principal"}, Note: groupsNote},
		},
		"filter by group matches groupPrincipalName and groupName": {
			params:   listCRTBParams{Cluster: "local", Group: "github_team://1234"},
			objects:  []runtime.Object{crtb1, crtbGroup, crtbLegacyGroup},
			expected: resultSummary{Names: []string{"crtb-group", "crtb-legacy-group"}},
		},
		"user and group cannot both be specified": {
			params:        listCRTBParams{Cluster: "local", User: "u-user1", Group: "github_team://1234"},
			objects:       []runtime.Object{crtb1},
			expectedError: "only one of user or group can be specified",
		},
		"no CRTBs found": {
			params:   listCRTBParams{Cluster: "local"},
			objects:  []runtime.Object{},
			expected: resultSummary{Note: "no resources found"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newTestTools(t, append(tt.objects, testUsers()...)...)

			result, _, err := tools.listClusterRoleTemplateBindings(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), tt.params)
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
