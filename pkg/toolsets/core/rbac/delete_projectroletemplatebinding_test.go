package rbac

import (
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const noBindingText = "role 'project-member' is not granted in project 'local:p-web', no changes needed"

func terminatingPRTB() *unstructured.Unstructured {
	prtb := newMgmtObject("ProjectRoleTemplateBinding", "local-p-web", "prtb-terminating", map[string]any{
		"projectName": "local:p-web", "roleTemplateName": "project-member", "userName": "u-alice",
	})
	prtb.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	prtb.SetFinalizers([]string{"controller.cattle.io/mgmt-auth-prtb-controller"})
	return prtb
}

func lockedRolePRTB() *unstructured.Unstructured {
	return newMgmtObject("ProjectRoleTemplateBinding", "local-p-web", "prtb-locked-role", map[string]any{
		"projectName": "local:p-web", "roleTemplateName": "locked-role", "userName": "u-alice",
	})
}

func TestDeleteProjectRoleTemplateBinding(t *testing.T) {
	tests := map[string]struct {
		params         deletePRTBParams
		extraObjects   []runtime.Object
		expectedResult string
		expectedPRTBs  int
		expectedError  string
	}{
		"remove role from user": {
			params:         deletePRTBParams{Cluster: "local", Project: "web", User: "alice", RoleTemplate: "project-owner"},
			expectedResult: "role 'project-owner' removed in project 'local:p-web' by deleting project role template binding(s) local-p-web/prtb-existing",
			expectedPRTBs:  0,
		},
		"remove role from locked role template": {
			params:         deletePRTBParams{Cluster: "local", Project: "web", User: "alice", RoleTemplate: "locked-role"},
			extraObjects:   []runtime.Object{lockedRolePRTB()},
			expectedResult: "role 'locked-role' removed in project 'local:p-web' by deleting project role template binding(s) local-p-web/prtb-locked-role",
			expectedPRTBs:  1,
		},
		"role not granted": {
			params:         deletePRTBParams{Cluster: "local", Project: "web", User: "alice"},
			expectedResult: noBindingText,
			expectedPRTBs:  1,
		},
		"remove role from user bound by principal": {
			params: deletePRTBParams{Cluster: "local", Project: "web", User: "alice"},
			extraObjects: []runtime.Object{newMgmtObject("ProjectRoleTemplateBinding", "local-p-web", "prtb-principal", map[string]any{
				"projectName": "local:p-web", "roleTemplateName": "project-member", "userPrincipalName": "local://u-alice",
			})},
			expectedResult: "role 'project-member' removed in project 'local:p-web' by deleting project role template binding(s) local-p-web/prtb-principal",
			expectedPRTBs:  1,
		},
		"remove role from group bound by group name": {
			params: deletePRTBParams{Cluster: "local", Project: "web", Group: "developers"},
			extraObjects: []runtime.Object{newMgmtObject("ProjectRoleTemplateBinding", "local-p-web", "prtb-group", map[string]any{
				"projectName": "local:p-web", "roleTemplateName": "project-member", "groupName": "developers",
			})},
			expectedResult: "role 'project-member' removed in project 'local:p-web' by deleting project role template binding(s) local-p-web/prtb-group",
			expectedPRTBs:  1,
		},
		"binding already being deleted": {
			params:         deletePRTBParams{Cluster: "local", Project: "web", User: "alice"},
			extraObjects:   []runtime.Object{terminatingPRTB()},
			expectedResult: noBindingText,
			expectedPRTBs:  2,
		},
		"unknown user": {
			params:        deletePRTBParams{Cluster: "local", Project: "web", User: "bob"},
			expectedError: "user 'bob' not found",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeDynClient := newPRTBTestTools(tt.extraObjects...)

			result, _, err := tools.deleteProjectRoleTemplateBinding(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)

			prtbs, err := fakeDynClient.Resource(converter.K8sKindsToGVRs["projectroletemplatebinding"]).
				Namespace("local-p-web").List(t.Context(), metav1.ListOptions{})
			require.NoError(t, err)
			assert.Len(t, prtbs.Items, tt.expectedPRTBs)
		})
	}
}

func TestDeleteProjectRoleTemplateBindingPlan(t *testing.T) {
	tests := map[string]struct {
		params         deletePRTBParams
		extraObjects   []runtime.Object
		expectedResult string
		expectedPRTBs  int
	}{
		"plan removing role from user": {
			params:         deletePRTBParams{Cluster: "local", Project: "web", User: "alice", RoleTemplate: "project-owner"},
			expectedResult: `[{"type": "delete", "payload": null, "resource": {"name": "prtb-existing", "kind": "ProjectRoleTemplateBinding", "cluster": "local", "namespace": "local-p-web"}}]`,
			expectedPRTBs:  1,
		},
		"plan removing role from locked role template": {
			params:         deletePRTBParams{Cluster: "local", Project: "web", User: "alice", RoleTemplate: "locked-role"},
			extraObjects:   []runtime.Object{lockedRolePRTB()},
			expectedResult: `[{"type": "delete", "payload": null, "resource": {"name": "prtb-locked-role", "kind": "ProjectRoleTemplateBinding", "cluster": "local", "namespace": "local-p-web"}}]`,
			expectedPRTBs:  2,
		},
		"plan for role not granted": {
			params:         deletePRTBParams{Cluster: "local", Project: "web", User: "alice"},
			expectedResult: noBindingText,
			expectedPRTBs:  1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeDynClient := newPRTBTestTools(tt.extraObjects...)

			result, _, err := tools.deleteProjectRoleTemplateBindingPlan(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)

			require.NoError(t, err)
			text := result.Content[0].(*mcp.TextContent).Text
			if tt.expectedResult == noBindingText {
				assert.Equal(t, tt.expectedResult, text)
			} else {
				assert.JSONEq(t, tt.expectedResult, text)
			}

			prtbs, err := fakeDynClient.Resource(converter.K8sKindsToGVRs["projectroletemplatebinding"]).
				Namespace("local-p-web").List(t.Context(), metav1.ListOptions{})
			require.NoError(t, err)
			assert.Len(t, prtbs.Items, tt.expectedPRTBs, "plan must not delete resources")
		})
	}
}
