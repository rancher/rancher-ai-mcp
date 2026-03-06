package core

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

func TestListClusters(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = metav1.AddMetaToScheme(scheme)

	customListKinds := map[schema.GroupVersionResource]string{
		{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}: "ClusterList",
	}

	t.Run("list multiple clusters", func(t *testing.T) {
		cluster1 := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "management.cattle.io/v3",
				"kind":       "Cluster",
				"metadata": map[string]any{
					"name": "cluster-1",
				},
			},
		}

		cluster2 := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "management.cattle.io/v3",
				"kind":       "Cluster",
				"metadata": map[string]any{
					"name": "cluster-2",
				},
			},
		}

		fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, customListKinds, cluster1, cluster2)

		c := &client.Client{
			DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
				return fakeDynClient, nil
			},
		}
		tools := Tools{client: newFakeToolsClient(c, fakeToken)}

		result, _, err := tools.listClusters(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{
			Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {fakeUrl}}},
		}, struct{}{})

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		expectedResult := `{
    "llm": [
        {
            "apiVersion": "management.cattle.io/v3",
            "kind": "Cluster",
            "metadata": {
                "name": "cluster-1"
            }
        },
        {
            "apiVersion": "management.cattle.io/v3",
            "kind": "Cluster",
            "metadata": {
                "name": "cluster-2"
            }
        }
    ],
    "uiContext": [
        {
            "cluster": "local",
            "kind": "Cluster",
            "name": "cluster-1",
            "namespace": "",
            "type": "management.cattle.io.cluster"
        },
        {
            "cluster": "local",
            "kind": "Cluster",
            "name": "cluster-2",
            "namespace": "",
            "type": "management.cattle.io.cluster"
        }
    ]
}`
		text := result.Content[0].(*mcp.TextContent).Text
		assert.JSONEq(t, expectedResult, text)
	})
}
