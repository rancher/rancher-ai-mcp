package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// updateKubernetesResourceParams defines the structure for updating a general Kubernetes resource.
// It includes fields required to uniquely identify a resource within a cluster.
type updateKubernetesResourceParams struct {
	Name      string      `json:"name" jsonschema:"the name of the specific resource to patch"`
	Namespace string      `json:"namespace,omitempty" jsonschema:"the namespace where the resource is located. It must be empty for cluster-wide resources"`
	Kind      string      `json:"kind" jsonschema:"the type of Kubernetes resource to patch (e.g., Pod, Deployment, Service)"`
	Cluster   string      `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Patch     []jsonPatch `json:"patch" jsonschema:"the patch to apply. The content type used is application/json-patch+json"`
}

// updateKubernetesResource updates a specific Kubernetes resource using a JSON patch.
func (t *Tools) updateKubernetesResource(ctx context.Context, toolReq *mcp.CallToolRequest, params updateKubernetesResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("updateKubernetesResource called")

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), t.rancherURL(toolReq), params.Namespace, params.Cluster, converter.K8sKindsToGVRs[strings.ToLower(params.Kind)])
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
