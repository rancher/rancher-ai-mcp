package provisioning

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// newManagementNode creates a test Rancher management node object, which lives in a namespace named after its cluster.
func newManagementNode(name, clusterID, kubernetesNodeName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Node",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": clusterID,
				"labels": map[string]interface{}{
					"example": "label",
				},
			},
			"spec": map[string]interface{}{
				"worker": true,
			},
			"status": map[string]interface{}{
				"nodeName": kubernetesNodeName,
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "True",
					},
				},
				"internalNodeStatus": map[string]interface{}{
					"capacity": map[string]interface{}{"cpu": "4"},
				},
			},
		},
	}
}

func TestIsManagementNode(t *testing.T) {
	tests := map[string]struct {
		obj      *unstructured.Unstructured
		expected bool
	}{
		"management node": {
			obj:      newManagementNode("m-abc", "c-abc", "node-1"),
			expected: true,
		},
		"kubernetes node": {
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Node",
			}},
			expected: false,
		},
		"management cluster": {
			obj:      newManagementCluster("c-abc", true),
			expected: false,
		},
		"CAPI machine": {
			obj:      newCAPIMachine("machine", "fleet-default", "cluster", "Running", ""),
			expected: false,
		},
		"object without a kind": {
			obj:      &unstructured.Unstructured{Object: map[string]interface{}{}},
			expected: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isManagementNode(tt.obj))
		})
	}
}

func TestTrimManagementNode(t *testing.T) {
	t.Run("keeps only the identity, node name and conditions", func(t *testing.T) {
		node := newManagementNode("m-abc", "c-abc", "node-1")

		trimmed := trimManagementNode(node)

		assert.Equal(t, map[string]interface{}{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Node",
			"metadata": map[string]interface{}{
				"name":      "m-abc",
				"namespace": "c-abc",
			},
			"status": map[string]interface{}{
				"nodeName": "node-1",
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "True",
					},
				},
			},
		}, trimmed.Object)
	})

	t.Run("does not modify the original node", func(t *testing.T) {
		node := newManagementNode("m-abc", "c-abc", "node-1")
		expected := node.DeepCopy()

		trimmed := trimManagementNode(node)
		assert.NotSame(t, node, trimmed)

		// changing the trimmed copy must not leak into the original
		conditions, found, err := unstructured.NestedSlice(trimmed.Object, "status", "conditions")
		assert.NoError(t, err)
		assert.True(t, found)
		conditions[0].(map[string]interface{})["status"] = "False"
		assert.NoError(t, unstructured.SetNestedSlice(trimmed.Object, conditions, "status", "conditions"))

		assert.Equal(t, expected.Object, node.Object)
	})

	t.Run("a node without optional fields is returned without them", func(t *testing.T) {
		node := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Node",
			"metadata": map[string]interface{}{
				"name":      "m-abc",
				"namespace": "c-abc",
			},
			"spec": map[string]interface{}{"worker": true},
		}}

		trimmed := trimManagementNode(node)

		assert.Equal(t, map[string]interface{}{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Node",
			"metadata": map[string]interface{}{
				"name":      "m-abc",
				"namespace": "c-abc",
			},
		}, trimmed.Object)
	})

	t.Run("a node with only conditions or only a node name", func(t *testing.T) {
		onlyName := newManagementNode("m-abc", "c-abc", "node-1")
		unstructured.RemoveNestedField(onlyName.Object, "status", "conditions")
		_, found, _ := unstructured.NestedFieldNoCopy(trimManagementNode(onlyName).Object, "status", "conditions")
		assert.False(t, found)
		nodeName, found, _ := unstructured.NestedString(trimManagementNode(onlyName).Object, "status", "nodeName")
		assert.True(t, found)
		assert.Equal(t, "node-1", nodeName)

		onlyConditions := newManagementNode("m-abc", "c-abc", "node-1")
		unstructured.RemoveNestedField(onlyConditions.Object, "status", "nodeName")
		_, found, _ = unstructured.NestedString(trimManagementNode(onlyConditions).Object, "status", "nodeName")
		assert.False(t, found)
		_, found, _ = unstructured.NestedFieldNoCopy(trimManagementNode(onlyConditions).Object, "status", "conditions")
		assert.True(t, found)
	})
}
