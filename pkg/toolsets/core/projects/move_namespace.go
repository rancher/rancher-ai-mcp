package projects

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type moveNamespaceParams struct {
	Namespace string `json:"namespace" jsonschema:"the name of the namespace to move"`
	Project   string `json:"project" jsonschema:"the name or ID of the destination project"`
	Cluster   string `json:"cluster" jsonschema:"the name of the cluster containing the namespace and project"`
}

// moveNamespace assigns a namespace to a project.
func (t *Tools) moveNamespace(ctx context.Context, toolReq *mcp.CallToolRequest, params moveNamespaceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("moveNamespace called", zap.String("namespace", params.Namespace), zap.String("project", params.Project), zap.String("cluster", params.Cluster))

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		return nil, nil, err
	}

	projectID, _, err := t.getProjectID(ctx, middleware.Token(ctx), clusterID, params.Project)
	if err != nil {
		return nil, nil, err
	}

	namespace, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: clusterID,
		Kind:    "namespace",
		Name:    params.Namespace,
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get namespace %q: %w", params.Namespace, err)
	}

	labels := namespace.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["field.cattle.io/projectId"] = projectID
	namespace.SetLabels(labels)

	annotations := namespace.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations["field.cattle.io/projectId"] = fmt.Sprintf("%s:%s", clusterID, projectID)
	namespace.SetAnnotations(annotations)

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx), "", clusterID, converter.K8sKindsToGVRs["namespace"])
	if err != nil {
		return nil, nil, err
	}

	updatedNamespace, err := resourceInterface.Update(ctx, namespace, metav1.UpdateOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to move namespace %q: %w", params.Namespace, err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{updatedNamespace}, clusterID)
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
