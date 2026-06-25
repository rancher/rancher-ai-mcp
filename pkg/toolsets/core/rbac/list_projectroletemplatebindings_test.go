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

func TestListProjectRoleTemplateBindings(t *testing.T) {
	prtb1 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "ProjectRoleTemplateBinding",
			"metadata": map[string]any{
				"name":      "prtb-1",
				"namespace": "local-p-abc",
			},
			"projectName":      "local:p-abc",
			"userName":         "u-user1",
			"roleTemplateName": "project-owner",
		},
	}
	prtb2 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "ProjectRoleTemplateBinding",
			"metadata": map[string]any{
				"name":      "prtb-2",
				"namespace": "local-p-abc",
			},
			"projectName":      "local:p-abc",
			"userName":         "u-user2",
			"roleTemplateName": "project-member",
		},
	}
	prtb3 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "ProjectRoleTemplateBinding",
			"metadata": map[string]any{
				"name":      "prtb-3",
				"namespace": "local-p-xyz",
			},
			"projectName":      "local:p-xyz",
			"userName":         "u-user1",
			"roleTemplateName": "project-member",
		},
	}

	tests := map[string]struct {
		params         listPRTBParams
		objects        []runtime.Object
		expectedResult string
	}{
		"list all PRTBs for a cluster": {
			params:  listPRTBParams{Cluster: "local"},
			objects: []runtime.Object{prtb1, prtb2, prtb3},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-1", "namespace": "local-p-abc"},
						"projectName": "local:p-abc",
						"roleTemplateName": "project-owner",
						"userName": "u-user1"
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-2", "namespace": "local-p-abc"},
						"projectName": "local:p-abc",
						"roleTemplateName": "project-member",
						"userName": "u-user2"
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-3", "namespace": "local-p-xyz"},
						"projectName": "local:p-xyz",
						"roleTemplateName": "project-member",
						"userName": "u-user1"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-1", "namespace": "local-p-abc", "type": "projectroletemplatebinding"},
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-2", "namespace": "local-p-abc", "type": "projectroletemplatebinding"},
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-3", "namespace": "local-p-xyz", "type": "projectroletemplatebinding"}
				]
			}`,
		},
		"filter by project": {
			params:  listPRTBParams{Cluster: "local", Project: "p-abc"},
			objects: []runtime.Object{prtb1, prtb2, prtb3},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-1", "namespace": "local-p-abc"},
						"projectName": "local:p-abc",
						"roleTemplateName": "project-owner",
						"userName": "u-user1"
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-2", "namespace": "local-p-abc"},
						"projectName": "local:p-abc",
						"roleTemplateName": "project-member",
						"userName": "u-user2"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-1", "namespace": "local-p-abc", "type": "projectroletemplatebinding"},
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-2", "namespace": "local-p-abc", "type": "projectroletemplatebinding"}
				]
			}`,
		},
		"filter by user": {
			params:  listPRTBParams{Cluster: "local", User: "u-user1"},
			objects: []runtime.Object{prtb1, prtb2, prtb3},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-1", "namespace": "local-p-abc"},
						"projectName": "local:p-abc",
						"roleTemplateName": "project-owner",
						"userName": "u-user1"
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-3", "namespace": "local-p-xyz"},
						"projectName": "local:p-xyz",
						"roleTemplateName": "project-member",
						"userName": "u-user1"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-1", "namespace": "local-p-abc", "type": "projectroletemplatebinding"},
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-3", "namespace": "local-p-xyz", "type": "projectroletemplatebinding"}
				]
			}`,
		},
		"filter by project and user": {
			params:  listPRTBParams{Cluster: "local", Project: "p-abc", User: "u-user1"},
			objects: []runtime.Object{prtb1, prtb2, prtb3},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "ProjectRoleTemplateBinding",
						"metadata": {"name": "prtb-1", "namespace": "local-p-abc"},
						"projectName": "local:p-abc",
						"roleTemplateName": "project-owner",
						"userName": "u-user1"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ProjectRoleTemplateBinding", "name": "prtb-1", "namespace": "local-p-abc", "type": "projectroletemplatebinding"}
				]
			}`,
		},
		"no PRTBs found": {
			params:         listPRTBParams{Cluster: "local"},
			objects:        []runtime.Object{},
			expectedResult: `{"llm": "no resources found"}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(rbacScheme(), rbacGVRs, tt.objects...)
			c := &client.Client{
				DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) { return fakeDynClient, nil },
			}
			tools := NewTools(test.WrapClient(c, fakeToken, fakeURL), fakeURL, false)

			result, _, err := tools.listProjectRoleTemplateBindings(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
