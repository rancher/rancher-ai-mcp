package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// jsonPatch represents a JSON Patch operation as defined in RFC 6902.
// It specifies an operation (add, remove, replace, etc.) to be applied to a JSON document.
type jsonPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// jsonPatchList is a list of JSON Patch operations. It implements a lenient
// json.Unmarshaler so it can accept either a proper JSON array of patch
// operations or a JSON string containing a stringified array. Some LLMs send
// the patch as a stringified JSON array instead of a real array, so we support
// both forms transparently.
type jsonPatchList []jsonPatch

// UnmarshalJSON accepts both a JSON array of patch operations and a JSON string
// containing a stringified JSON array.
func (p *jsonPatchList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)

	// If the payload is a JSON string, unquote it and parse its contents as the
	// actual patch array.
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return fmt.Errorf("failed to unmarshal patch string: %w", err)
		}
		trimmed = bytes.TrimSpace([]byte(str))

		// Some LLMs additionally wrap the stringified array in single quotes
		// (e.g. '[{"op":"replace",...}]'). Strip a matching pair of surrounding
		// single quotes before parsing.
		if len(trimmed) >= 2 && trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'' {
			trimmed = bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
		}
	}

	var patches []jsonPatch
	if err := json.Unmarshal(trimmed, &patches); err != nil {
		return fmt.Errorf("failed to unmarshal patch as JSON array: %w", err)
	}

	*p = patches
	return nil
}

// updateKubernetesResourceParams defines the structure for updating a general Kubernetes resource.
// It includes fields required to uniquely identify a resource within a cluster.
type updateKubernetesResourceParams struct {
	Name      string        `json:"name" jsonschema:"the name of the specific resource to patch"`
	Namespace string        `json:"namespace,omitempty" jsonschema:"the namespace where the resource is located. It must be empty for cluster-wide resources"`
	Kind      string        `json:"kind" jsonschema:"the type of Kubernetes resource to patch (e.g., Pod, Deployment, Service)"`
	Cluster   string        `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Patch     jsonPatchList `json:"patch" jsonschema:"a JSON array of patch operation objects. Each element must be an object with 'op', 'path', and optionally 'value' fields, as defined in RFC 6902 (application/json-patch+json). Prefer a real JSON array; a stringified array is also accepted. Example: [{\"op\":\"replace\",\"path\":\"/spec/replicas\",\"value\":3}]"`
}

// patchResourceInputSchema builds the input schema for the patch tools. It is
// derived from updateKubernetesResourceParams but relaxes the "patch" property
// so schema validation accepts both a real JSON array and a stringified JSON
// array, since some LLMs send the patch as a JSON string instead of an array.
// The lenient jsonPatchList.UnmarshalJSON normalizes the string form after
// validation passes.
func patchResourceInputSchema() *jsonschema.Schema {
	s, err := jsonschema.For[updateKubernetesResourceParams](nil)
	if err != nil {
		panic(fmt.Errorf("failed to build patch resource input schema: %w", err))
	}

	if patch, ok := s.Properties["patch"]; ok {
		arraySchema := *patch
		arraySchema.Description = ""
		// jsonschema-go infers a slice as type ["null", "array"]. Some agent
		// clients reject the multi-type form, so force a single "array" type.
		arraySchema.Type = "array"
		arraySchema.Types = nil
		s.Properties["patch"] = &jsonschema.Schema{
			Description: patch.Description,
			AnyOf: []*jsonschema.Schema{
				&arraySchema,
				{Type: "string"},
			},
		}
	}

	return s
}

// updateKubernetesResource updates a specific Kubernetes resource using a JSON patch.
func (t *Tools) updateKubernetesResource(ctx context.Context, toolReq *mcp.CallToolRequest, params updateKubernetesResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("updateKubernetesResource called")

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), params.Namespace, params.Cluster, converter.K8sKindsToGVRs[strings.ToLower(params.Kind)])
	if err != nil {
		return nil, nil, err
	}

	patchBytes, err := json.Marshal(params.Patch)
	if err != nil {
		zap.L().Error("failed to create patch", zap.String("tool", "updateKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	obj, err := resourceInterface.Patch(ctx, params.Name, types.JSONPatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		zap.L().Error("failed to apply patch", zap.String("tool", "updateKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to patch resource %s: %w", params.Name, err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "updateKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
