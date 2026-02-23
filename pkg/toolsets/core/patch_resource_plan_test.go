package core

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateKubernetesResourcePlan(t *testing.T) {
	tests := map[string]struct {
		params         updateKubernetesResourceParams
		expectedResult string
		expectedError  string
	}{
		"update configmap plan - add new key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "ConfigMap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "add",
						Path:  "/data/key3",
						Value: "value3",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": [{"op": "add", "path": "/data/key3", "value": "value3"}],
				"resource": {
					"name": "test-config",
					"kind": "ConfigMap",
					"cluster": "local",
					"namespace": "default"
				}
			}]`,
		},
		"update configmap plan - replace existing key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "ConfigMap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "replace",
						Path:  "/data/key1",
						Value: "updated-value",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": [{"op": "replace", "path": "/data/key1", "value": "updated-value"}],
				"resource": {
					"name": "test-config",
					"kind": "ConfigMap",
					"cluster": "local",
					"namespace": "default"
				}
			}]`,
		},
		"update configmap plan - remove key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "ConfigMap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:   "remove",
						Path: "/data/key2",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": [{"op": "remove", "path": "/data/key2"}],
				"resource": {
					"name": "test-config",
					"kind": "ConfigMap",
					"cluster": "local",
					"namespace": "default"
				}
			}]`,
		},
		"update plan - multiple patches": {
			params: updateKubernetesResourceParams{
				Name:      "my-deploy",
				Namespace: "staging",
				Kind:      "Deployment",
				Cluster:   "downstream",
				Patch: []jsonPatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 3,
					},
					{
						Op:    "add",
						Path:  "/metadata/labels/env",
						Value: "staging",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": [
					{"op": "replace", "path": "/spec/replicas", "value": 3},
					{"op": "add", "path": "/metadata/labels/env", "value": "staging"}
				],
				"resource": {
					"name": "my-deploy",
					"kind": "Deployment",
					"cluster": "downstream",
					"namespace": "staging"
				}
			}]`,
		},
		"update plan - cluster-scoped resource": {
			params: updateKubernetesResourceParams{
				Name:      "my-ns",
				Namespace: "",
				Kind:      "Namespace",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "add",
						Path:  "/metadata/labels/team",
						Value: "platform",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": [{"op": "add", "path": "/metadata/labels/team", "value": "platform"}],
				"resource": {
					"name": "my-ns",
					"kind": "Namespace",
					"cluster": "local",
					"namespace": ""
				}
			}]`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			tools := Tools{}

			result, _, err := tools.updateKubernetesResourcePlan(t.Context(), &mcp.CallToolRequest{}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, test.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
