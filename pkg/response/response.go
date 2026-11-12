package response

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/rancher/rancher-ai-mcp/pkg/converter"
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

// serverMetadataFields are the metadata fields maintained by the API server and controllers.
// They are not relevant to the resource itself, so they are removed from responses.
var serverMetadataFields = []string{"managedFields", "uid", "resourceVersion", "generation", "finalizers", "ownerReferences"}

// internalAnnotationPrefixes are the prefixes of annotations that Rancher's controllers use to
// track their own state.
var internalAnnotationPrefixes = []string{"lifecycle.cattle.io/", "authz.management.cattle.io/", "cleanup.cattle.io/"}

// RemoveServerManagedFields removes the metadata fields maintained by the API server and controllers,
// the last applied configuration annotation, and the annotations Rancher's controllers use to track
// their own state, so responses only show what is relevant to the resource.
// The generated name is also removed once the resource has a name.
func RemoveServerManagedFields(obj *unstructured.Unstructured) {
	for _, field := range serverMetadataFields {
		unstructured.RemoveNestedField(obj.Object, "metadata", field)
	}

	// The generated name is only needed to identify a resource that has not been named yet.
	if obj.GetName() != "" {
		unstructured.RemoveNestedField(obj.Object, "metadata", "generateName")
	}

	annotations, found, err := unstructured.NestedMap(obj.Object, "metadata", "annotations")
	if err != nil || !found {
		return
	}

	delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")

	for key := range annotations {
		for _, prefix := range internalAnnotationPrefixes {
			if strings.HasPrefix(key, prefix) {
				delete(annotations, key)

				break
			}
		}
	}

	if len(annotations) == 0 {
		unstructured.RemoveNestedField(obj.Object, "metadata", "annotations")

		return
	}

	_ = unstructured.SetNestedMap(obj.Object, annotations, "metadata", "annotations")
}

// Option configures the response built by CreateMcpResponse.
type Option func(*responseOptions)

type responseOptions struct {
	notes           []string
	summarizeStatus bool
}

// WithNotes adds notes to the response. When notes are provided, the LLM field is wrapped as
// {"resources": ..., "note": "..."}, with the notes joined by newlines.
func WithNotes(notes ...string) Option {
	return func(o *responseOptions) {
		o.notes = append(o.notes, notes...)
	}
}

// WithSummarizedStatus reduces the status of each resource to what can explain a problem with it.
// Conditions that are True are removed, as are timestamps and observed generations, so a resource
// that has been reconciled keeps only its summary.
//
// This is only suitable for resources whose conditions are True when the resource is healthy.
// For some resources a True condition reports a problem, e.g. a Job's Failed condition or a
// Node's DiskPressure condition, so this would remove it.
func WithSummarizedStatus() Option {
	return func(o *responseOptions) {
		o.summarizeStatus = true
	}
}

// CreateMcpResponse constructs an MCPResponse object. It takes a slice of unstructured Kubernetes objects,
// the cluster they are in, and options. The fields maintained by the server are removed from each object
// with RemoveServerManagedFields, and the objects are modified in place.
func CreateMcpResponse(objs []*unstructured.Unstructured, cluster string, opts ...Option) (string, error) {
	var options responseOptions
	for _, opt := range opts {
		opt(&options)
	}

	var uiContext []UIContext

	for _, obj := range objs {
		RemoveServerManagedFields(obj)

		if options.summarizeStatus {
			summarizeStatus(obj)
		}

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

	if note := strings.Join(options.notes, "\n"); note != "" {
		data = map[string]any{
			"resources": data,
			"note":      note,
		}
	}

	return CreateMcpResponseAny(data, uiContext...)
}

// summarizeStatus reduces the status of a resource to what can explain a problem with it.
// See WithSummarizedStatus.
func summarizeStatus(obj *unstructured.Unstructured) {
	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if err != nil || !found {
		return
	}

	for key, value := range status {
		switch {
		case key == "lastUpdateTime", strings.HasPrefix(key, "observedGeneration"):
			delete(status, key)
		case strings.HasSuffix(strings.ToLower(key), "conditions"):
			conditions, ok := value.([]any)
			if !ok {
				continue
			}

			var problems []any

			for _, condition := range conditions {
				if conditionMap, ok := condition.(map[string]any); ok && conditionMap["status"] == "True" {
					continue
				}

				problems = append(problems, condition)
			}

			if len(problems) == 0 {
				delete(status, key)
			} else {
				status[key] = problems
			}
		default:
			// Other status fields, such as the summary, are kept.
		}
	}

	if len(status) == 0 {
		unstructured.RemoveNestedField(obj.Object, "status")
		return
	}

	_ = unstructured.SetNestedMap(obj.Object, status, "status")
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
// It pairs an operation type (create, update or delete) with the target resource
// metadata and the operation payload.
type PlanResource struct {
	// Type is the operation to perform on the resource.
	Type OperationType `json:"type" jsonschema:"the operation to perform on the resource: create, update or delete"`
	// Payload holds the resource body for create and delete operations or the patch data for update operations.
	Payload any `json:"payload"`
	// Resource identifies the target Kubernetes resource.
	Resource Resource `json:"resource"`
	// Detail is shown to the user with the confirmation of the operation, e.g. to warn
	// about the effects of the operation on other resources.
	Detail string `json:"detail,omitempty" jsonschema:"text to show to the user with the confirmation of the operation"`
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

// NewDeleteResourceInput constructs a PlanResource for a DELETE operation.
// It extracts the resource metadata from the given unstructured object and
// sets the full object as the payload so the resource being deleted can be displayed.
func NewDeleteResourceInput(obj *unstructured.Unstructured, cluster string) PlanResource {
	planResource := PlanResource{
		Type: OperationDelete,
		Resource: Resource{
			Name:      obj.GetName(),
			Kind:      obj.GetKind(),
			Cluster:   cluster,
			Namespace: obj.GetNamespace(),
		},
		Payload: obj,
	}

	return planResource
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

// NoChangesPlanType is the type of a plan response indicating that no resources need to be created, updated or deleted.
const NoChangesPlanType = "no_changes"

// NoChangesPlan is returned by a plan tool instead of a list of PlanResources when the requested
// change is already in place, so that the agent can skip the confirmation and report the message.
type NoChangesPlan struct {
	// Type is always NoChangesPlanType.
	Type string `json:"type"`
	// Message explains why no changes are needed.
	Message string `json:"message"`
}

// CreateNoChangesPlanResponse serializes a NoChangesPlan with the given message into a JSON string.
func CreateNoChangesPlanResponse(message string) (string, error) {
	bytes, err := json.Marshal(NoChangesPlan{Type: NoChangesPlanType, Message: message})
	if err != nil {
		return "", fmt.Errorf("failed to marshal no changes plan response: %w", err)
	}

	return string(bytes), nil
}
