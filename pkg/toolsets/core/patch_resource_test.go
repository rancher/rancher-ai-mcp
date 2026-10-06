package core

import (
	"encoding/json"
	"fmt"
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
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return tt.fakeDynClient, nil
				},
			}
			tools := NewTools(test.WrapClient(c, fakeToken), false)
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

func TestJsonPatchListUnmarshalJSON(t *testing.T) {
	expected := jsonPatchList{
		{Op: "replace", Path: "/spec/replicas", Value: float64(3)},
		{Op: "add", Path: "/metadata/labels/env", Value: "prod"},
	}

	tests := map[string]struct {
		input string
	}{
		"normal JSON array": {
			input: `[{"op":"replace","path":"/spec/replicas","value":3},{"op":"add","path":"/metadata/labels/env","value":"prod"}]`,
		},
		"JSON string containing stringified array": {
			input: `"[{\"op\":\"replace\",\"path\":\"/spec/replicas\",\"value\":3},{\"op\":\"add\",\"path\":\"/metadata/labels/env\",\"value\":\"prod\"}]"`,
		},
		"JSON string containing stringified array wrapped in single quotes": {
			input: `"'[{\"op\":\"replace\",\"path\":\"/spec/replicas\",\"value\":3},{\"op\":\"add\",\"path\":\"/metadata/labels/env\",\"value\":\"prod\"}]'"`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var got jsonPatchList
			err := json.Unmarshal([]byte(tt.input), &got)
			require.NoError(t, err)
			assert.Equal(t, expected, got)
		})
	}
}

// TestPatchResourceInputSchemaNoBooleanSubschema asserts that the
// patchKubernetesResource input schema contains no boolean subschemas.
// Boolean subschemas (e.g. "value": true) are valid JSON Schema 2020-12 but
// are rejected by strict OpenAPI-style validators used by some
// OpenAI-compatible providers (e.g. Volcano Engine Ark, error 11133), which
// makes those providers refuse any request carrying the tool declarations.
func TestPatchResourceInputSchemaNoBooleanSubschema(t *testing.T) {
	s := patchResourceInputSchema()

	assert.Equal(t, "array", s.Properties["patch"].Type)

	raw, err := json.Marshal(s)
	require.NoError(t, err)

	// Positions where a boolean acts as a permissive subschema ("accept
	// anything"). "additionalProperties: false" is also a boolean subschema
	// but a restrictive one that OpenAI-style validators themselves emit, so
	// only permissive positions are checked.
	permissiveKeys := map[string]bool{"items": true, "value": true, "properties": true}

	var walk func(t *testing.T, path string, v any)
	walk = func(t *testing.T, path string, v any) {
		switch node := v.(type) {
		case map[string]any:
			for k, child := range node {
				if b, ok := child.(bool); ok && (permissiveKeys[k] || (k == "additionalProperties" && b)) {
					t.Errorf("%s.%s: boolean subschema %v — strict providers reject boolean subschemas in permissive positions", path, k, b)
					continue
				}
				walk(t, path+"."+k, child)
			}
		case []any:
			for i, child := range node {
				walk(t, fmt.Sprintf("%s[%d]", path, i), child)
			}
		}
	}

	var decoded any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	walk(t, "$", decoded)
}
