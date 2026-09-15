package projects

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

func TestMoveNamespace(t *testing.T) {
	const (
		clusterID   = "c-move-namespace"
		projectID   = "p-move-namespace"
		projectName = "Destination Project"
		namespace   = "workloads"
		fakeToken   = "fakeToken"
	)

	cluster := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": clusterID},
	}}
	project := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Project",
		"metadata":   map[string]any{"name": projectID, "namespace": clusterID},
		"spec":       map[string]any{"displayName": projectName},
	}}
	namespaceResource := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"field.cattle.io/projectId": "p-previous",
				"team":                      "platform",
			},
			Annotations: map[string]string{
				"field.cattle.io/projectId": "c-previous:p-previous",
				"example.com/owner":         "platform",
			},
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, metav1.AddMetaToScheme(scheme))
	fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}: "ClusterList",
		{Group: "management.cattle.io", Version: "v3", Resource: "projects"}: "ProjectList",
	}, cluster, project, namespaceResource)

	c := &client.Client{DynClientCreator: func(*rest.Config) (dynamic.Interface, error) {
		return fakeDynClient, nil
	}}
	tools := NewTools(newFakeToolsClient(c, fakeToken), false)

	result, _, err := tools.moveNamespace(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{}, moveNamespaceParams{
		Namespace: namespace,
		Project:   projectName,
		Cluster:   clusterID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.JSONEq(t, `{
		"llm": [{
			"apiVersion": "v1",
			"kind": "Namespace",
			"metadata": {
				"name": "workloads",
				"labels": {
					"field.cattle.io/projectId": "p-move-namespace",
					"team": "platform"
				},
				"annotations": {
					"field.cattle.io/projectId": "c-move-namespace:p-move-namespace",
					"example.com/owner": "platform"
				}
			},
			"spec": {},
			"status": {}
		}],
		"uiContext": [{
			"cluster": "c-move-namespace",
			"kind": "Namespace",
			"name": "workloads",
			"namespace": "",
			"type": "namespace"
		}]
	}`, result.Content[0].(*mcp.TextContent).Text)
}
