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

func TestGetUser(t *testing.T) {
	user1 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "User",
			"metadata": map[string]any{
				"name": "u-abc123",
			},
			"username":    "admin",
			"displayName": "Default Admin",
		},
	}
	user2 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "User",
			"metadata": map[string]any{
				"name": "u-xyz456",
			},
			"username":    "jsmith",
			"displayName": "John Smith",
		},
	}

	tests := map[string]struct {
		params         getUserParams
		objects        []runtime.Object
		expectedResult string
	}{
		"get user by username": {
			params:  getUserParams{Username: "admin"},
			objects: []runtime.Object{user1, user2},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "User",
						"metadata": {"name": "u-abc123"},
						"username": "admin",
						"displayName": "Default Admin"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "User", "name": "u-abc123", "namespace": "", "type": "user"}
				]
			}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(rbacScheme(), rbacGVRs, tt.objects...)
			c := &client.Client{
				DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) { return fakeDynClient, nil },
			}
			tools := NewTools(test.WrapClient(c, fakeToken, fakeURL), fakeURL, false)

			result, _, err := tools.getUser(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
