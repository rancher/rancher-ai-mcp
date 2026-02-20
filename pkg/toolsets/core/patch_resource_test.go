package core

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

var fakeConfigMapForPatch = &corev1.ConfigMap{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-config",
		Namespace: "default",
	},
	Data: map[string]string{
		"key1": "value1",
		"key2": "value2",
	},
}

func patchResourceScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func TestUpdateKubernetesResource(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params        updateKubernetesResourceParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"update configmap - add new key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "add",
						Path:  "/data/key3",
						Value: "value3",
					},
				},
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(patchResourceScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
			}, fakeConfigMapForPatch),
			requestURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "v1",
						"data": {"key1": "value1", "key2": "value2", "key3": "value3"},
						"kind": "ConfigMap",
						"metadata": {"name": "test-config", "namespace": "default"}
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ConfigMap", "name": "test-config", "namespace": "default", "type": "configmap"}
				]
			}`,
		},
		"update configmap - replace existing key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "replace",
						Path:  "/data/key1",
						Value: "updated-value",
					},
				},
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(patchResourceScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
			}, fakeConfigMapForPatch),
			requestURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "v1",
						"data": {"key1": "updated-value", "key2": "value2"},
						"kind": "ConfigMap",
						"metadata": {"name": "test-config", "namespace": "default"}
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ConfigMap", "name": "test-config", "namespace": "default", "type": "configmap"}
				]
			}`,
		},
		"update configmap - remove key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:   "remove",
						Path: "/data/key2",
					},
				},
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(patchResourceScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
			}, fakeConfigMapForPatch),
			requestURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "v1",
						"data": {"key1": "value1"},
						"kind": "ConfigMap",
						"metadata": {"name": "test-config", "namespace": "default"}
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ConfigMap", "name": "test-config", "namespace": "default", "type": "configmap"}
				]
			}`,
		},
		"update configmap when tool is configured with URL": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "replace",
						Path:  "/data/key1",
						Value: "updated-value",
					},
				},
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(patchResourceScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
			}, fakeConfigMapForPatch),
			rancherURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "v1",
						"data": {"key1": "updated-value", "key2": "value2"},
						"kind": "ConfigMap",
						"metadata": {"name": "test-config", "namespace": "default"}
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "ConfigMap", "name": "test-config", "namespace": "default", "type": "configmap"}
				]
			}`,
		},
		"update configmap - not found": {
			params: updateKubernetesResourceParams{
				Name:      "nonexistent-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "replace",
						Path:  "/data/key1",
						Value: "value",
					},
				},
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(patchResourceScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
			}),
			requestURL:    fakeUrl,
			expectedError: `configmaps "nonexistent-config" not found`,
		},
		"update configmap - no rancherURL or request URL": {
			params: updateKubernetesResourceParams{
				Name:      "nonexistent-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "replace",
						Path:  "/data/key1",
						Value: "value",
					},
				},
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(patchResourceScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
			}),
			expectedError: "no URL for rancher request",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return tt.fakeDynClient, nil
				},
			}
			tools := NewTools(test.WrapClient(c, fakeToken, fakeUrl), tt.rancherURL)
			req := test.NewCallToolRequest(tt.requestURL)

			result, _, err := tools.updateKubernetesResource(middleware.WithToken(t.Context(), fakeToken), req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
