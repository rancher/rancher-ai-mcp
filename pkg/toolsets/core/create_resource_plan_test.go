package core

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateKubernetesResourcePlan(t *testing.T) {
	configMapResource := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      "test-config",
			"namespace": "default",
		},
		"data": map[string]interface{}{
			"key1": "value1",
			"key2": "value2",
		},
	}

	tests := map[string]struct {
		params         createKubernetesResourceParams
		expectedResult string
		expectedError  string
	}{
		"create configmap plan": {
			params: createKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Resource:  configMapResource,
			},
			expectedResult: `[{
				"type": "create",
				"payload": {
					"apiVersion": "v1",
					"kind": "ConfigMap",
					"metadata": {"name": "test-config", "namespace": "default"},
					"data": {"key1": "value1", "key2": "value2"}
				},
				"resource": {
					"name": "test-config",
					"kind": "ConfigMap",
					"cluster": "local",
					"namespace": "default"
				}
			}]`,
		},
		"create plan - marshal error": {
			params: createKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Resource:  make(chan int),
			},
			expectedError: "failed to marshal resource",
		},
		"create plan - invalid resource": {
			params: createKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "configmap",
				Cluster:   "local",
				Resource:  "invalid-resource-type",
			},
			expectedError: "failed to create unstructured object",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			tools := Tools{}

			result, _, err := tools.createKubernetesResourcePlan(t.Context(), &mcp.CallToolRequest{}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, test.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
