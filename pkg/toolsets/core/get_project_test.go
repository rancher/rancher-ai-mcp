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

func TestGetProject(t *testing.T) {
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

	project := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Project",
			"metadata": map[string]any{
				"name":      "my-project",
				"namespace": "test-cluster",
			},
			"spec": map[string]any{
				"displayName": "My Project",
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

	t.Run("get project with namespaces", func(t *testing.T) {
		namespace1 := &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns-1",
				Labels: map[string]string{
					"field.cattle.io/projectId": "my-project",
				},
			},
		}

		namespace2 := &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "ns-2",
				Labels: map[string]string{
					"field.cattle.io/projectId": "my-project",
				},
			},
		}

		fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, customListKinds, cluster, project, namespace1, namespace2)

		c := &client.Client{
			DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
				return fakeDynClient, nil
			},
		}
		tools := Tools{client: newFakeToolsClient(c, fakeToken)}

		result, _, err := tools.getProject(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{
			Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {fakeUrl}}},
		}, getProjectParams{
			Name:    "my-project",
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
                "name": "my-project",
                "namespace": "test-cluster"
            },
            "spec": {
                "displayName": "My Project"
            }
        },
        {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {
                "labels": {
                    "field.cattle.io/projectId": "my-project"
                },
                "name": "ns-1"
            },
            "spec": {},
            "status": {}
        },
        {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {
                "labels": {
                    "field.cattle.io/projectId": "my-project"
                },
                "name": "ns-2"
            },
            "spec": {},
            "status": {}
        }
    ],
    "uiContext": [
        {
            "cluster": "test-cluster",
            "kind": "Project",
            "name": "my-project",
            "namespace": "test-cluster",
            "type": "project"
        },
        {
            "cluster": "test-cluster",
            "kind": "Namespace",
            "name": "ns-1",
            "namespace": "",
            "type": "namespace"
        },
        {
            "cluster": "test-cluster",
            "kind": "Namespace",
            "name": "ns-2",
            "namespace": "",
            "type": "namespace"
        }
    ]
}`
		text := result.Content[0].(*mcp.TextContent).Text
		t.Logf("got result: %s", text)
		assert.JSONEq(t, expectedResult, text)
	})

	t.Run("project not found", func(t *testing.T) {
		fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, customListKinds, cluster)

		c := &client.Client{
			DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
				return fakeDynClient, nil
			},
		}
		tools := Tools{client: newFakeToolsClient(c, fakeToken)}

		_, _, err := tools.getProject(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{
			Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {fakeUrl}}},
		}, getProjectParams{
			Name:    "nonexistent-project",
			Cluster: "test-cluster",
		})

		assert.ErrorContains(t, err, "not found")
	})

	t.Run("project with no namespaces", func(t *testing.T) {
		fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, customListKinds, cluster, project)

		c := &client.Client{
			DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
				return fakeDynClient, nil
			},
		}
		tools := Tools{client: newFakeToolsClient(c, fakeToken)}

		result, _, err := tools.getProject(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{
			Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {fakeUrl}}},
		}, getProjectParams{
			Name:    "my-project",
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
                "name": "my-project",
                "namespace": "test-cluster"
            },
            "spec": {
                "displayName": "My Project"
            }
        }
    ],
    "uiContext": [
        {
            "cluster": "test-cluster",
            "kind": "Project",
            "name": "my-project",
            "namespace": "test-cluster",
            "type": "project"
        }
    ]
}`
		text := result.Content[0].(*mcp.TextContent).Text
		assert.JSONEq(t, expectedResult, text)
	})
}
