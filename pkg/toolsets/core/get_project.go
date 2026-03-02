package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapGetProject = zap.String("tool", "getProject")

type getProjectParams struct {
	Name    string `json:"name" jsonschema:"the name of the project resource"`
	Cluster string `json:"cluster" jsonschema:"the cluster of the project resource"`
}

// getProjectID retrieves the project ID for a given project name.
func (t *Tools) getProjectID(ctx context.Context, token, url, clusterID, projectNameOrID string) (string, *unstructured.Unstructured, error) {
	projectResource, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   LocalCluster,
		Kind:      "project",
		Namespace: clusterID,
		Name:      projectNameOrID,
		URL:       url,
		Token:     token,
	})
	if err == nil {
		return projectResource.GetName(), projectResource, nil
	}

	if !apierrors.IsNotFound(err) {
		return "", nil, err
	}

	resources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   LocalCluster,
		Kind:      "project",
		Namespace: clusterID,
		URL:       url,
		Token:     token,
	})
	if err != nil {
		return "", nil, err
	}

	for _, resource := range resources {
		displayName, found, err := unstructured.NestedString(resource.Object, "spec", "displayName")
		if err != nil || !found {
			continue
		}

		if strings.EqualFold(displayName, projectNameOrID) {
			return resource.GetName(), resource, nil
		}
	}

	return "", nil, fmt.Errorf("project '%s' not found in cluster '%s'", projectNameOrID, clusterID)
}

// getProject retrieves a project resource.
func (t *Tools) getProject(ctx context.Context, toolReq *mcp.CallToolRequest, params getProjectParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getProject called")

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), t.rancherURL(toolReq), params.Cluster)
	if err != nil {
		zap.L().Error("failed to get cluster ID", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectID, projectResource, err := t.getProjectID(ctx, middleware.Token(ctx), t.rancherURL(toolReq), clusterID, params.Name)
	if err != nil {
		zap.L().Error("failed to get project", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectLabel, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{
			"field.cattle.io/projectId": projectID,
		},
	})
	if err != nil {
		zap.L().Error("failed to create label selector", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectNamespaces, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       clusterID,
		Kind:          "namespace",
		LabelSelector: projectLabel.String(),
		URL:           t.rancherURL(toolReq),
		Token:         middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get namespaces for project", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	resources := append([]*unstructured.Unstructured{projectResource}, projectNamespaces...)

	mcpResponse, err := response.CreateMcpResponse(resources, clusterID)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
