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

const projectIDKey = "field.cattle.io/projectId"

type moveNamespaceParams struct {
	Namespace string `json:"namespace" jsonschema:"the name of the namespace to move"`
	Project   string `json:"project" jsonschema:"the name or ID of the destination project"`
	Cluster   string `json:"cluster" jsonschema:"the name of the cluster containing the namespace and project"`
}

// moveNamespaceTarget holds the resolved data needed to move a namespace to a project.
type moveNamespaceTarget struct {
	clusterID  string
	projectID  string
	namespace  *unstructured.Unstructured
	label      string
	annotation string
}

// resolveMoveNamespace resolves the cluster and project IDs and fetches the namespace to be moved.
func (t *Tools) resolveMoveNamespace(ctx context.Context, params moveNamespaceParams) (*moveNamespaceTarget, error) {
	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		return nil, err
	}

	projectID, _, err := GetProjectID(ctx, t.client, middleware.Token(ctx), clusterID, params.Project)
	if err != nil {
		return nil, err
	}

	namespace, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: clusterID,
		Kind:    "namespace",
		Name:    params.Namespace,
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace %q: %w", params.Namespace, err)
	}

	return &moveNamespaceTarget{
		clusterID:  clusterID,
		projectID:  projectID,
		namespace:  namespace,
		label:      projectID,
		annotation: fmt.Sprintf("%s:%s", clusterID, projectID),
	}, nil
}

// moveNamespace assigns a namespace to a project.
func (t *Tools) moveNamespace(ctx context.Context, toolReq *mcp.CallToolRequest, params moveNamespaceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("moveNamespace called", zap.String("namespace", params.Namespace), zap.String("project", params.Project), zap.String("cluster", params.Cluster))

	target, err := t.resolveMoveNamespace(ctx, params)
	if err != nil {
		return nil, nil, err
	}
	namespace := target.namespace

	labels := namespace.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[projectIDKey] = target.label
	namespace.SetLabels(labels)

	annotations := namespace.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[projectIDKey] = target.annotation
	namespace.SetAnnotations(annotations)

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx), "", target.clusterID, converter.K8sKindsToGVRs["namespace"])
	if err != nil {
		return nil, nil, err
	}

	updatedNamespace, err := resourceInterface.Update(ctx, namespace, metav1.UpdateOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to move namespace %q: %w", params.Namespace, err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{updatedNamespace}, target.clusterID)
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
