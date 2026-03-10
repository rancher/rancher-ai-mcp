package response

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// UIContext holds the contextual information for a Kubernetes resource. Used to build links by the UI.
type UIContext struct {
	// Namespace is the Kubernetes namespace where the resources are located.
	Namespace string `json:"namespace" jsonschema:"the namespace of the resource"`
	// Kind is the type of the Kubernetes resource (e.g., "Pod", "Deployment").
	Kind string `json:"kind" jsonschema:"the kind of the resource"`
	// Cluster identifies the Rancher cluster where the resources reside.
	Cluster string `json:"cluster" jsonschema:"the cluster of the resource"`
	// Name is a string containing the name of the resource.
	Name string `json:"name" jsonschema:"the name of k8s resource"`
	// Type is a string representing the resource type in steve
	Type string `json:"type,omitempty"`
}

// MCPResponse represents the response returned by the MCP server
type MCPResponse struct {
	// LLM response to be sent to the LLM
	LLM any `json:"llm"`
	// UIContext contains a list of resources so the UI can generate links to them
	UIContext []UIContext `json:"uiContext,omitempty"`
}

// CreateMcpResponse constructs an MCPResponse object. It takes a slice of unstructured Kubernetes objects, namespace, kind, cluster,
// and optional additional information strings. It marshals the response into a JSON string.
func CreateMcpResponse(objs []*unstructured.Unstructured, cluster string) (string, error) {
	var uiContext []UIContext
	for _, obj := range objs {
		unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
		unstructured.RemoveNestedField(obj.Object, "metadata", "annotations", "kubectl.kubernetes.io/last-applied-configuration")

		gvk := obj.GetObjectKind().GroupVersionKind()
		lowerKind := strings.ToLower(gvk.Kind)
		if lowerKind == "" {
			continue
		}

		// use prefixes to differentiate duplicate kinds from different API groups
		// (e.g. cluster.x-k8s.io.cluster vs provisioning.cattle.io.cluster)
		lookupKind := lowerKind
		steveType := lowerKind
		switch gvk.Group {
		case converter.CAPIGroup:
			lookupKind = converter.CAPIKindPrefix + lookupKind
		case converter.ProvisioningGroup:
			lookupKind = converter.ProvisioningKindPrefix + lookupKind
		case converter.ManagementGroup:
			lookupKind = converter.ManagementKindPrefix + lookupKind
		case converter.MachineConfigGroup:
			// machine configs are dynamically generated from node drivers
			// using their name, so we can't maintain a mapping for all of them.
			// fortunately, its highly unlikely there will be a conflict across groups
			// so we just use the group directly.
			steveType = gvk.Group + "." + lowerKind
		}

		if gvr, ok := converter.K8sKindsToGVRs[lookupKind]; ok && gvr.Group != "" {
			steveType = gvr.Group + "." + lowerKind
		}

		uiContext = append(uiContext, UIContext{
			Namespace: obj.GetNamespace(),
			Kind:      obj.GetKind(),
			Cluster:   cluster,
			Name:      obj.GetName(),
			Type:      steveType,
		})
	}

	var data any = "no resources found"
	if len(objs) > 0 {
		data = objs
	}

	return CreateMcpResponseAny(data, uiContext...)
}

// CreateMcpResponseAny constructs an MCPResponse with any data that can be marshaled into JSON.
// This gives a full control over the shape of the returned data and the optional UI context.
func CreateMcpResponseAny(data any, uiContext ...UIContext) (string, error) {
	resp := MCPResponse{
		LLM:       data,
		UIContext: uiContext,
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(bytes), nil
}

// CreateMcpResponseWithAdditionalText constructs an MCPResponse that wraps the resource list
// inside an object together with an additional text note. The resulting JSON has the form:
// {"llm": {"resources": [...], "note": "..."}, "uiContext": [...]}.
func CreateMcpResponseWithAdditionalText(objs []*unstructured.Unstructured, cluster, additionalText string) (string, error) {
	baseResponse, err := CreateMcpResponse(objs, cluster)
	if err != nil {
		return "", err
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(baseResponse), &resp); err != nil {
		return "", fmt.Errorf("failed to unmarshal base response: %w", err)
	}

	resp["llm"] = map[string]any{
		"resources": resp["llm"],
		"note":      additionalText,
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response with additional text: %w", err)
	}

	return string(bytes), nil
}

// OperationType represents the type of operation in a plan
type OperationType string

const (
	// OperationCreate represents a resource creation operation
	OperationCreate OperationType = "create"
	// OperationUpdate represents a resource update operation
	OperationUpdate OperationType = "update"
	// OperationDelete represents a resource deletion operation
	OperationDelete OperationType = "delete"
)

// Resource identifies a Kubernetes resource by name, kind, cluster, and namespace.
type Resource struct {
	// Name is the name of the Kubernetes resource.
	Name string `json:"name"`
	// Kind is the type of the Kubernetes resource (e.g., "Pod", "Deployment").
	Kind string `json:"kind"`
	// Cluster is the Rancher cluster where the resource resides.
	Cluster string `json:"cluster"`
	// Namespace is the Kubernetes namespace of the resource.
	Namespace string `json:"namespace"`
}

// PlanResource represents a single planned operation on a Kubernetes resource.
// It pairs an operation type (CREATE, UPDATE, DELETE) with the target resource
// metadata and the operation payload.
type PlanResource struct {
	// Type is the operation to perform on the resource.
	Type OperationType `json:"type" jsonschema:"enum=CREATE,enum=UPDATE,enum=DELETE"`
	// Payload holds the resource body for CREATE operations or the patch data for UPDATE operations.
	Payload any `json:"payload"`
	// Resource identifies the target Kubernetes resource.
	Resource Resource `json:"resource"`
}

// NewCreateResourceInput constructs a PlanResource for a CREATE operation.
// It extracts the resource metadata from the given unstructured object and
// sets the full object as the payload.
func NewCreateResourceInput(obj *unstructured.Unstructured, cluster string) PlanResource {
	plan_resource := PlanResource{
		Type: OperationCreate,
		Resource: Resource{
			Name:      obj.GetName(),
			Kind:      obj.GetKind(),
			Cluster:   cluster,
			Namespace: obj.GetNamespace(),
		},
		Payload: obj,
	}

	return plan_resource
}

// NewUpdateResourceInput constructs a PlanResource for an UPDATE operation.
// It extracts the resource metadata from the given unstructured object and
// uses the provided patch bytes as the payload.
func NewUpdateResourceInput(obj *unstructured.Unstructured, patch []byte, cluster string) PlanResource {
	plan_resource := PlanResource{
		Type: OperationUpdate,
		Resource: Resource{
			Name:      obj.GetName(),
			Kind:      obj.GetKind(),
			Cluster:   cluster,
			Namespace: obj.GetNamespace(),
		},
		Payload: patch,
	}

	return plan_resource
}

// CreatePlanResponse serializes a slice of PlanResource into a JSON string.
// It returns the JSON representation and any marshalling error encountered.
func CreatePlanResponse(resources []PlanResource) (string, error) {
	bytes, err := json.Marshal(resources)
	if err != nil {
		return "", fmt.Errorf("failed to marshal plan response: %w", err)
	}

	return string(bytes), nil
}
