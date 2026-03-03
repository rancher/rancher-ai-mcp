package core

import (
	"context"
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
	Cluster           string `json:"cluster" jsonschema:"the cluster that the project belongs to"`
	Name              string `json:"name" jsonschema:"the name of the project to be created"`
	Description       string `json:"description,omitempty" jsonschema:"an optional description for the project"`
	DisplayName       string `json:"displayName,omitempty" jsonschema:"an optional display name for the project"`
	CPULimit          int    `json:"cpuLimit,omitempty" jsonschema:"the maximum amount of CPU resources (mCPUs) that can be used by containers in the project"`
	CPUReservation    int    `json:"cpuReservation,omitempty" jsonschema:"the amount of CPU resources (mCPUs) reserved for containers in the project"`
	MemoryLimit       int    `json:"memoryLimit,omitempty" jsonschema:"the maximum amount of memory resources (MiB) that can be used by containers in the project"`
	MemoryReservation int    `json:"memoryReservation,omitempty" jsonschema:"the amount of memory resources (MiB) reserved for containers in the project"`
}

func (t *Tools) createProject(ctx context.Context, toolReq *mcp.CallToolRequest, params createProjectParams) (*mcp.CallToolResult, any, error) {
	zap.L().Info("createProject called", zap.String("cluster", params.Cluster))

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx), t.rancherURL(toolReq),
		params.Cluster, "local", converter.K8sKindsToGVRs["project"])
	if err != nil {
		return nil, nil, err
	}

	project, err := t.createProjectObj(params)
	if err != nil {
		zap.L().Error("failed to create project object", zap.String("tool", "createProject"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create project object: %w", err)
	}

	obj, err := resourceInterface.Create(ctx, project, metav1.CreateOptions{})
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

func (t *Tools) createProjectObj(params createProjectParams) (*unstructured.Unstructured, error) {
	project := &unstructured.Unstructured{
		Object: make(map[string]any),
	}
	project.SetKind("Project")
	project.SetAPIVersion(converter.ManagementGroup + "/v3")

	project.SetName(params.Name)
	project.SetNamespace(params.Cluster)
	if err := unstructured.SetNestedField(project.Object, params.Cluster, "spec", "clusterName"); err != nil {
		return nil, fmt.Errorf("failed to set project cluster name: %w", err)
	}

	if params.Description != "" {
		if err := unstructured.SetNestedField(project.Object, params.Description, "spec", "description"); err != nil {
			return nil, fmt.Errorf("failed to set project description: %w", err)
		}
	}
	if params.DisplayName != "" {
		if err := unstructured.SetNestedField(project.Object, params.DisplayName, "spec", "displayName"); err != nil {
			return nil, fmt.Errorf("failed to set project display name: %w", err)
		}
	}

	// Create any container resource quotas if specified with their respective units
	containerResourceQuotas := make(map[string]any)
	if params.CPULimit != 0 {
		containerResourceQuotas["limitsCpu"] = fmt.Sprintf("%dm", params.CPULimit)
	}
	if params.CPUReservation != 0 {
		containerResourceQuotas["requestsCpu"] = fmt.Sprintf("%dm", params.CPUReservation)
	}
	if params.MemoryLimit != 0 {
		containerResourceQuotas["limitsMemory"] = fmt.Sprintf("%dMi", params.MemoryLimit)
	}
	if params.MemoryReservation != 0 {
		containerResourceQuotas["requestsMemory"] = fmt.Sprintf("%dMi", params.MemoryReservation)
	}

	if err := unstructured.SetNestedField(project.Object, containerResourceQuotas, "spec", "containerDefaultResourceLimit"); err != nil {
		return nil, fmt.Errorf("failed to set project container resource quotas: %w", err)
	}
	zap.L().Info("project object created successfully", zap.Any("projectObject", project))

	return project, nil
}
