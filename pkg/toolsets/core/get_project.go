package core

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapGetProject = zap.String("tool", "getProject")

type getProjectParams struct {
	Name    string `json:"name" jsonschema:"the name of the project resource"`
	Cluster string `json:"cluster" jsonschema:"the cluster of the project resource"`
}

// getProject retrieves a project resource.
func (t *Tools) getProject(ctx context.Context, toolReq *mcp.CallToolRequest, params getProjectParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getProject called")

	projectResource, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   "local",
		Kind:      "project",
		Namespace: params.Cluster,
		Name:      params.Name,
		URL:       toolReq.Extra.Header.Get(urlHeader),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get project", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectLabel, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{
			"field.cattle.io/projectId": params.Name,
		},
	})
	if err != nil {
		zap.L().Error("failed to create label selector", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectNamespaces, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       params.Cluster,
		Kind:          "namespace",
		LabelSelector: projectLabel.String(),
		URL:           toolReq.Extra.Header.Get(urlHeader),
		Token:         middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get namespaces for project", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	resources := append([]*unstructured.Unstructured{projectResource}, projectNamespaces...)

	mcpResponse, err := response.CreateMcpResponse(resources, params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
