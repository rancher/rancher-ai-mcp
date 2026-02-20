package core

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// specificResourceParams uniquely identifies a resource with a known kind within a cluster.
type specificResourceParams struct {
	Name      string `json:"name" jsonschema:"the name of k8s resource"`
	Namespace string `json:"namespace" jsonschema:"the namespace of the resource"`
	Cluster   string `json:"cluster" jsonschema:"the cluster of the resource"`
}

// getDeploymentDetails retrieves details about a deployment and its associated pods.
func (t *Tools) getDeploymentDetails(ctx context.Context, toolReq *mcp.CallToolRequest, params specificResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getDeploymentDetails called")

	deploymentResource, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   params.Cluster,
		Kind:      "deployment",
		Namespace: params.Namespace,
		Name:      params.Name,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get deployment", zap.String("tool", "getDeploymentDetails"), zap.Error(err))
		return nil, nil, err
	}

	var deployment appsv1.Deployment
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(deploymentResource.Object, &deployment); err != nil {
		zap.L().Error("failed convert unstructured object to Deployment", zap.String("tool", "getDeploymentDetails"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to convert unstructured object to Pod: %w", err)
	}

	// find all pods for this deployment
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		zap.L().Error("failed create label selector", zap.String("tool", "getDeploymentDetails"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to convert label selector: %w", err)
	}
	pods, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       params.Cluster,
		Kind:          "pod",
		Namespace:     params.Namespace,
		Name:          params.Name,
		URL:           t.rancherURL(toolReq),
		Token:         middleware.Token(ctx),
		LabelSelector: selector.String(),
	})
	if err != nil {
		zap.L().Error("failed to get pods", zap.String("tool", "getDeploymentDetails"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to get pods: %w", err)
	}

	mcpResponse, err := response.CreateMcpResponse(append([]*unstructured.Unstructured{deploymentResource}, pods...), params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "getDeploymentDetails"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
