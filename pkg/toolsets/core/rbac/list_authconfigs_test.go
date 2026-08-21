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

func TestListAuthConfigs(t *testing.T) {
	localConfig := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "AuthConfig",
			"metadata": map[string]any{
				"name": "local",
			},
			"type":    "localConfig",
			"enabled": true,
		},
	}
	adConfig := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "AuthConfig",
			"metadata": map[string]any{
				"name": "activedirectory",
			},
			"type":    "activeDirectoryConfig",
			"enabled": false,
		},
	}

	tests := map[string]struct {
		objects        []runtime.Object
		expectedResult string
	}{
		"list multiple auth configs": {
			objects: []runtime.Object{localConfig, adConfig},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "AuthConfig",
						"metadata": {"name": "activedirectory"},
						"type": "activeDirectoryConfig",
						"enabled": false
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "AuthConfig",
						"metadata": {"name": "local"},
						"type": "localConfig",
						"enabled": true
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "AuthConfig", "name": "activedirectory", "namespace": "", "type": "authconfig"},
					{"cluster": "local", "kind": "AuthConfig", "name": "local", "namespace": "", "type": "authconfig"}
				]
			}`,
		},
		"no auth configs": {
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
			tools := NewTools(test.WrapClient(c, fakeToken), false)

			result, _, err := tools.listAuthConfigs(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				struct{}{},
			)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
