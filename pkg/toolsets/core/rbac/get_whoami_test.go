package rbac

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetestesting "k8s.io/client-go/testing"

	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestWhoAmI(t *testing.T) {
	user := &unstructured.Unstructured{
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

	tests := map[string]struct {
		userInfo       authenticationv1.UserInfo
		objects        []runtime.Object
		expectedResult string
	}{
		"identity matches an existing user": {
			userInfo: authenticationv1.UserInfo{
				Username: "u-abc123",
				UID:      "u-abc123",
				Groups:   []string{"system:authenticated"},
			},
			objects: []runtime.Object{user},
			expectedResult: `{
				"llm": [
					{"username": "u-abc123", "uid": "u-abc123", "groups": ["system:authenticated"]},
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
		"identity does not match any known user": {
			userInfo: authenticationv1.UserInfo{
				Username: "u-unknown",
				UID:      "u-unknown",
				Groups:   []string{"system:authenticated"},
			},
			objects: []runtime.Object{user},
			expectedResult: `{
				"llm": [
					{"username": "u-unknown", "uid": "u-unknown", "groups": ["system:authenticated"]}
				]
			}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(rbacScheme(), rbacGVRs, tt.objects...)

			fakeClientset := kubernetesfake.NewSimpleClientset()
			fakeClientset.PrependReactor("create", "selfsubjectreviews", func(_ kubernetestesting.Action) (bool, runtime.Object, error) {
				return true, &authenticationv1.SelfSubjectReview{
					Status: authenticationv1.SelfSubjectReviewStatus{UserInfo: tt.userInfo},
				}, nil
			})

			c := &client.Client{
				DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) { return fakeDynClient, nil },
				ClientSetCreator: func(_ *rest.Config) (kubernetes.Interface, error) { return fakeClientset, nil },
			}
			tools := NewTools(test.WrapClient(c, fakeToken), false)

			result, _, err := tools.whoAmI(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				struct{}{},
			)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
