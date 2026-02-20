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
)

const (
	urlHeader = "R_url"
)

// createKubernetesResourceParams defines the structure for creating a general Kubernetes resource.
type createKubernetesResourceParams struct {
	Name      string `json:"name" jsonschema:"the name of k8s resource"`
	Namespace string `json:"namespace" jsonschema:"the namespace of the resource"`
	Kind      string `json:"kind" jsonschema:"the kind of the resource"`
	Cluster   string `json:"cluster" jsonschema:"the cluster of the resource"`
	Resource  any    `json:"resource" jsonschema:"the resource to be created"`
}

// createKubernetesResource creates a new Kubernetes resource.
func (t *Tools) createKubernetesResource(ctx context.Context, toolReq *mcp.CallToolRequest, params createKubernetesResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createKubernetesResource called")

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx), t.rancherURL(toolReq),
		params.Namespace, params.Cluster, converter.K8sKindsToGVRs[strings.ToLower(params.Kind)])
	if err != nil {
		return nil, nil, err
	}

	objBytes, err := json.Marshal(params.Resource)
	if err != nil {
		zap.L().Error("failed to marshal resource", zap.String("tool", "createKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal resource: %w", err)
	}

	unstructuredObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(objBytes, unstructuredObj); err != nil {
		zap.L().Error("failed to create unstructured resource", zap.String("tool", "createKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create unstructured object: %w", err)
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
