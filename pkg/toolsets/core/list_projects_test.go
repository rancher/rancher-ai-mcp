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

func TestListProjects(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	cluster := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Cluster",
			"metadata": map[string]any{
				"name": "test-cluster",
			},
			"status": map[string]any{
				"clusterName": "test-cluster",
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = metav1.AddMetaToScheme(scheme)

	customListKinds := map[schema.GroupVersionResource]string{
		{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}: "ClusterList",
		{Group: "management.cattle.io", Version: "v3", Resource: "projects"}: "ProjectList",
	}

	t.Run("list multiple projects", func(t *testing.T) {
		project1 := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "management.cattle.io/v3",
				"kind":       "Project",
				"metadata": map[string]any{
					"name":      "project-1",
					"namespace": "test-cluster",
				},
				"spec": map[string]any{
					"displayName": "Project 1",
				},
			},
		}

		project2 := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "management.cattle.io/v3",
				"kind":       "Project",
				"metadata": map[string]any{
					"name":      "project-2",
					"namespace": "test-cluster",
				},
				"spec": map[string]any{
					"displayName": "Project 2",
				},
			},
		}

		fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, customListKinds, cluster, project1, project2)

		c := &client.Client{
			DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
				return fakeDynClient, nil
			},
		}
		tools := Tools{client: newFakeToolsClient(c, fakeToken)}

		result, _, err := tools.listProjects(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{
			Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {fakeUrl}}},
		}, listProjectsParams{
			Cluster: "test-cluster",
		})

		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result.Content, 1)

		expectedResult := `{
    "llm": [
        {
            "apiVersion": "management.cattle.io/v3",
            "kind": "Project",
            "metadata": {
                "name": "project-1",
                "namespace": "test-cluster"
            },
            "spec": {
                "displayName": "Project 1"
            }
        },
        {
            "apiVersion": "management.cattle.io/v3",
            "kind": "Project",
            "metadata": {
                "name": "project-2",
                "namespace": "test-cluster"
            },
            "spec": {
                "displayName": "Project 2"
            }
        }
    ],
    "uiContext": [
        {
            "cluster": "test-cluster",
            "kind": "Project",
            "name": "project-1",
            "namespace": "test-cluster",
            "type": "project"
        },
        {
            "cluster": "test-cluster",
            "kind": "Project",
            "name": "project-2",
            "namespace": "test-cluster",
            "type": "project"
        }
    ]
}`
		text := result.Content[0].(*mcp.TextContent).Text
		assert.JSONEq(t, expectedResult, text)
	})
}
