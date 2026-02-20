package core

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// resourceParams uniquely identifies a specific named resource within a cluster.
type resourceParams struct {
	Name      string `json:"name" jsonschema:"the name of k8s resource"`
	Namespace string `json:"namespace" jsonschema:"the namespace of the resource"`
	Kind      string `json:"kind" jsonschema:"the kind of the resource"`
	Cluster   string `json:"cluster" jsonschema:"the cluster of the resource"`
}

// getResource retrieves a specific Kubernetes resource based on the provided parameters.
func (t *Tools) getResource(ctx context.Context, toolReq *mcp.CallToolRequest, params resourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getKubernetesResource called")

	resource, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   params.Cluster,
		Kind:      params.Kind,
		Namespace: params.Namespace,
		Name:      params.Name,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get resource", zap.String("tool", "getKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{resource}, params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "listKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
