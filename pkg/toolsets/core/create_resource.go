package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// createKubernetesResourceParams defines the structure for creating a general Kubernetes resource.
type createKubernetesResourceParams struct {
	Name      string `json:"name" jsonschema:"the name of the resource to create"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace where the resource is located. It must be empty for cluster-wide resources"`
	Kind      string `json:"kind" jsonschema:"the type of Kubernetes resource (e.g., Pod, Deployment, Service)"`
	Cluster   string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Manifest  string `json:"manifest" jsonschema:"the resource to create as a complete Kubernetes manifest, in YAML or JSON (e.g. \"apiVersion: v1\\nkind: ConfigMap\\nmetadata:\\n  name: my-cm\")"`
}

// createKubernetesResource creates a new Kubernetes resource.
func (t *Tools) createKubernetesResource(ctx context.Context, toolReq *mcp.CallToolRequest, params createKubernetesResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createKubernetesResource called")

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx),
		params.Namespace, params.Cluster, converter.K8sKindsToGVRs[strings.ToLower(params.Kind)])
	if err != nil {
		return nil, nil, err
	}

	unstructuredObj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(params.Manifest), unstructuredObj); err != nil {
		zap.L().Error("failed to parse manifest", zap.String("tool", "createKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to parse manifest (expected YAML or JSON): %w", err)
	}

	obj, err := resourceInterface.Create(ctx, unstructuredObj, metav1.CreateOptions{})
	if err != nil {
		zap.L().Error("failed to create resource", zap.String("tool", "createKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create resource %s: %w", params.Name, err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "createKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
