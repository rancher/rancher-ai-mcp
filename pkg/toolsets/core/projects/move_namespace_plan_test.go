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

func TestMoveNamespacePlan(t *testing.T) {
	const (
		clusterID   = "c-move-namespace"
		projectID   = "p-move-namespace"
		projectName = "Destination Project"
		fakeToken   = "fakeToken"
	)

	tests := map[string]struct {
		namespace    *corev1.Namespace
		expectedPlan string
	}{
		"namespace already assigned to a project": {
			namespace: &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
				ObjectMeta: metav1.ObjectMeta{
					Name:        "workloads",
					Labels:      map[string]string{"field.cattle.io/projectId": "p-previous", "team": "platform"},
					Annotations: map[string]string{"field.cattle.io/projectId": "c-previous:p-previous"},
				},
			},
			expectedPlan: `[{
				"type": "update",
				"payload": [
					{"op": "replace", "path": "/metadata/labels/field.cattle.io~1projectId", "value": "p-move-namespace"},
					{"op": "replace", "path": "/metadata/annotations/field.cattle.io~1projectId", "value": "c-move-namespace:p-move-namespace"}
				],
				"resource": {"name": "workloads", "kind": "Namespace", "cluster": "c-move-namespace", "namespace": ""}
			}]`,
		},
		"namespace without project and existing labels": {
			namespace: &corev1.Namespace{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
				ObjectMeta: metav1.ObjectMeta{
					Name:   "workloads",
					Labels: map[string]string{"team": "platform"},
				},
			},
			expectedPlan: `[{
				"type": "update",
				"payload": [
					{"op": "add", "path": "/metadata/labels/field.cattle.io~1projectId", "value": "p-move-namespace"},
					{"op": "add", "path": "/metadata/annotations", "value": {"field.cattle.io/projectId": "c-move-namespace:p-move-namespace"}}
				],
				"resource": {"name": "workloads", "kind": "Namespace", "cluster": "c-move-namespace", "namespace": ""}
			}]`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
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

			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, metav1.AddMetaToScheme(scheme))
			fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
				{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}: "ClusterList",
				{Group: "management.cattle.io", Version: "v3", Resource: "projects"}: "ProjectList",
			}, cluster, project, tt.namespace)

			c := &client.Client{DynClientCreator: func(*rest.Config) (dynamic.Interface, error) {
				return fakeDynClient, nil
			}}
			tools := NewTools(newFakeToolsClient(c, fakeToken), false)

			result, _, err := tools.moveNamespacePlan(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{}, moveNamespaceParams{
				Namespace: tt.namespace.Name,
				Project:   projectName,
				Cluster:   clusterID,
			})

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.JSONEq(t, tt.expectedPlan, result.Content[0].(*mcp.TextContent).Text)

			// The plan must not modify the namespace.
			ns, err := fakeDynClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).Get(t.Context(), tt.namespace.Name, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, tt.namespace.Labels, ns.GetLabels())
		})
	}
}
