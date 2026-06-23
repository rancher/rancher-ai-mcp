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

func TestGetRoleTemplate(t *testing.T) {
	rt := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "RoleTemplate",
			"metadata": map[string]any{
				"name": "project-owner",
			},
			"rules": []any{
				map[string]any{"verbs": []any{"*"}, "resources": []any{"*"}, "apiGroups": []any{"*"}},
			},
		},
	}

	tests := map[string]struct {
		params         getRoleTemplateParams
		objects        []runtime.Object
		expectedError  string
		expectedResult string
	}{
		"get role template by name": {
			params:  getRoleTemplateParams{Name: "project-owner"},
			objects: []runtime.Object{rt},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "RoleTemplate",
						"metadata": {"name": "project-owner"},
						"rules": [{"verbs": ["*"], "resources": ["*"], "apiGroups": ["*"]}]
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "RoleTemplate", "name": "project-owner", "namespace": "", "type": "roletemplate"}
				]
			}`,
		},
		"role template not found returns error": {
			params:        getRoleTemplateParams{Name: "nonexistent"},
			objects:       []runtime.Object{rt},
			expectedError: "not found",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(rbacScheme(), rbacGVRs, tt.objects...)
			c := &client.Client{
				DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) { return fakeDynClient, nil },
			}
			tools := NewTools(test.WrapClient(c, fakeToken), false)

			result, _, err := tools.getRoleTemplate(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
