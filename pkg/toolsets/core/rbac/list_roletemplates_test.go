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

func TestListRoleTemplates(t *testing.T) {
	rt1 := &unstructured.Unstructured{
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
	rt2 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "RoleTemplate",
			"metadata": map[string]any{
				"name": "project-member",
			},
			"rules": []any{
				map[string]any{"verbs": []any{"get", "list"}, "resources": []any{"pods"}, "apiGroups": []any{""}},
			},
		},
	}

	tests := map[string]struct {
		objects        []runtime.Object
		expectedResult string
	}{
		"list multiple role templates": {
			objects: []runtime.Object{rt1, rt2},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "RoleTemplate",
						"metadata": {"name": "project-member"},
						"rules": [{"verbs": ["get", "list"], "resources": ["pods"], "apiGroups": [""]}]
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "RoleTemplate",
						"metadata": {"name": "project-owner"},
						"rules": [{"verbs": ["*"], "resources": ["*"], "apiGroups": ["*"]}]
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "RoleTemplate", "name": "project-member", "namespace": "", "type": "roletemplate"},
					{"cluster": "local", "kind": "RoleTemplate", "name": "project-owner", "namespace": "", "type": "roletemplate"}
				]
			}`,
		},
		"no role templates": {
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

			result, _, err := tools.listRoleTemplates(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				struct{}{},
			)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
