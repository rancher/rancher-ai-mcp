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

func TestListProjectRoleTemplateBindings(t *testing.T) {
	prtb1 := prtb("prtb-1", "p-abc", "local:p-abc", "project-owner", "userName", "u-user1")
	prtb2 := prtb("prtb-2", "p-abc", "local:p-abc", "project-member", "userName", "u-user2")
	prtb3 := prtb("prtb-3", "local-p-xyz", "local:p-xyz", "project-member", "userName", "u-user1")
	prtbGroup := prtb("prtb-group", "local-p-xyz", "local:p-xyz", "project-member", "groupPrincipalName", "github_team://1234")
	projectABC := managementObject("Project", "p-abc", "local", map[string]any{
		"status": map[string]any{"backingNamespace": "p-abc"},
	})
	groupsNote := "includes bindings for the groups the user belongs to: github_team://1234"

	tests := map[string]struct {
		params         listPRTBParams
		objects        []runtime.Object
		expected       resultSummary
		expectedResult string
		expectedError  string
	}{
		"list all PRTBs for a cluster": {
			params:  listPRTBParams{Cluster: "local"},
			objects: []runtime.Object{prtb1, prtb2, prtb3},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-3", "namespace": "local-p-xyz"},
						"projectName": "local:p-xyz",
						"roleTemplateName": "project-member",
						"userName": "u-user1"
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-1", "namespace": "p-abc"},
						"projectName": "local:p-abc",
						"roleTemplateName": "project-owner",
						"userName": "u-user1"
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-2", "namespace": "p-abc"},
						"projectName": "local:p-abc",
						"roleTemplateName": "project-member",
						"userName": "u-user2"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-3", "namespace": "local-p-xyz", "type": "projectroletemplatebinding"},
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-1", "namespace": "p-abc", "type": "projectroletemplatebinding"},
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-2", "namespace": "p-abc", "type": "projectroletemplatebinding"}
				]
			}`,
		},
		"filter by project": {
			params:   listPRTBParams{Cluster: "local", ProjectID: "p-abc"},
			objects:  []runtime.Object{projectABC, prtb1, prtb2, prtb3},
			expected: resultSummary{Names: []string{"prtb-1", "prtb-2"}},
		},
		"filter by user includes bindings for the user's groups": {
			params:   listPRTBParams{Cluster: "local", User: "u-user1"},
			objects:  []runtime.Object{prtb1, prtb2, prtb3, prtbGroup},
			expected: resultSummary{Names: []string{"prtb-3", "prtb-group", "prtb-1"}, Note: groupsNote},
		},
		"filter by project and user": {
			params:   listPRTBParams{Cluster: "local", ProjectID: "p-abc", User: "user1"},
			objects:  []runtime.Object{projectABC, prtb1, prtb2, prtb3},
			expected: resultSummary{Names: []string{"prtb-1"}, Note: groupsNote},
		},
		"user and group cannot both be specified": {
			params:        listPRTBParams{Cluster: "local", User: "u-user1", Group: "github_team://1234"},
			objects:       []runtime.Object{prtb1},
			expectedError: "only one of user or group can be specified",
		},
		"no PRTBs found": {
			params:   listPRTBParams{Cluster: "local"},
			objects:  []runtime.Object{},
			expected: resultSummary{Note: "no resources found"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newTestTools(t, append(tt.objects, testUsers()...)...)

			result, _, err := tools.listProjectRoleTemplateBindings(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), tt.params)
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
