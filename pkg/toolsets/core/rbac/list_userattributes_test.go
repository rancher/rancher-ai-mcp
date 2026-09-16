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

func TestListUserAttributes(t *testing.T) {
	user1 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "User",
			"metadata": map[string]any{
				"name": "u-abc123",
			},
			"username": "admin",
		},
	}
	user2 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "User",
			"metadata": map[string]any{
				"name": "u-xyz456",
			},
			"username": "jsmith",
		},
	}
	attr1 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "UserAttribute",
			"metadata": map[string]any{
				"name": "u-abc123",
			},
			"lastLogin": "2026-08-01T00:00:00Z",
		},
	}
	attr2 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "UserAttribute",
			"metadata": map[string]any{
				"name": "u-xyz456",
			},
			"lastLogin": "2026-01-01T00:00:00Z",
		},
	}

	tests := map[string]struct {
		params         listUserAttributesParams
		objects        []runtime.Object
		expectedResult string
	}{
		"list all user attributes": {
			objects: []runtime.Object{user1, user2, attr1, attr2},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "UserAttribute",
						"metadata": {"name": "u-abc123"},
						"lastLogin": "2026-08-01T00:00:00Z"
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "UserAttribute",
						"metadata": {"name": "u-xyz456"},
						"lastLogin": "2026-01-01T00:00:00Z"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "UserAttribute", "name": "u-abc123", "namespace": "", "type": "userattribute"},
					{"cluster": "local", "kind": "UserAttribute", "name": "u-xyz456", "namespace": "", "type": "userattribute"}
				]
			}`,
		},
		"filter by username": {
			params:  listUserAttributesParams{Username: "admin"},
			objects: []runtime.Object{user1, user2, attr1, attr2},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "UserAttribute",
						"metadata": {"name": "u-abc123"},
						"lastLogin": "2026-08-01T00:00:00Z"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "UserAttribute", "name": "u-abc123", "namespace": "", "type": "userattribute"}
				]
			}`,
		},
		"filter by user id": {
			params:  listUserAttributesParams{Username: "u-xyz456"},
			objects: []runtime.Object{user1, user2, attr1, attr2},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "UserAttribute",
						"metadata": {"name": "u-xyz456"},
						"lastLogin": "2026-01-01T00:00:00Z"
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "UserAttribute", "name": "u-xyz456", "namespace": "", "type": "userattribute"}
				]
			}`,
		},
		"filter by unknown username returns no resources": {
			params:         listUserAttributesParams{Username: "nonexistent"},
			objects:        []runtime.Object{user1, user2, attr1, attr2},
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

			result, _, err := tools.listUserAttributes(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
