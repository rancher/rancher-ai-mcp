package provisioning

import (
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// isManagementNode reports whether the resource is a Rancher management node, as opposed to a Kubernetes node.
func isManagementNode(obj *unstructured.Unstructured) bool {
	gvk := obj.GroupVersionKind()
	return gvk.Group == converter.ManagementGroup && gvk.Kind == "Node"
}

// trimManagementNode returns a copy of a management node with only what identifies it and explains its health: its
// name, the name of the Kubernetes node, and its conditions. The complete object is large.
func trimManagementNode(node *unstructured.Unstructured) *unstructured.Unstructured {
	trimmed := &unstructured.Unstructured{Object: map[string]interface{}{}}
	trimmed.SetAPIVersion(node.GetAPIVersion())
	trimmed.SetKind(node.GetKind())
	trimmed.SetName(node.GetName())
	trimmed.SetNamespace(node.GetNamespace())

	// the fields are optional, so a node without them is returned without them.
	if nodeName, found, _ := unstructured.NestedString(node.Object, "status", "nodeName"); found {
		_ = unstructured.SetNestedField(trimmed.Object, nodeName, "status", "nodeName")
	}

	if conditions, found, _ := unstructured.NestedFieldCopy(node.Object, "status", "conditions"); found {
		_ = unstructured.SetNestedField(trimmed.Object, conditions, "status", "conditions")
	}

	return trimmed
}
