package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type createProjectParams struct {
	Cluster  string `json:"cluster" jsonschema:"the cluster where the project should be created"`
	Resource any    `json:"resource" jsonschema:"the project resource to be created"`
}

func (t *Tools) createProject(ctx context.Context, toolReq *mcp.CallToolRequest, params createProjectParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createProject called", zap.String("cluster", params.Cluster))

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx), t.rancherURL(toolReq),
		params.Cluster, "local", converter.K8sKindsToGVRs["project"])
	if err != nil {
		return nil, nil, err
	}

	objBytes, err := json.Marshal(params.Resource)
	if err != nil {
		zap.L().Error("failed to marshal resource", zap.String("tool", "createProject"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal resource: %w", err)
	}

	unstructuredObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(objBytes, unstructuredObj); err != nil {
		zap.L().Error("failed to create unstructured resource", zap.String("tool", "createProject"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create unstructured object: %w", err)
	}

	obj, err := resourceInterface.Create(ctx, unstructuredObj, metav1.CreateOptions{})
	if err != nil {
		zap.L().Error("failed to create project", zap.String("tool", "createProject"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create project: %w", err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, params.Cluster)
	if err != nil {
		zap.L().Error("failed to create MCP response", zap.String("tool", "createProject"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create MCP response: %w", err)
	}

	zap.L().Debug("project created successfully", zap.String("projectName", obj.GetName()), zap.String("cluster", params.Cluster))

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
