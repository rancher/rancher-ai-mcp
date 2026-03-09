package core

import (
	"context"
	"fmt"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type resourceQuota struct {
	Pods                   int            `json:"pods,omitempty" jsonschema:"the maximum number of pods that can be created in the project"`
	Services               int            `json:"services,omitempty" jsonschema:"the maximum number of services that can be created in the project"`
	ReplicationControllers int            `json:"replicationControllers,omitempty" jsonschema:"the maximum number of replication controllers that can be created in the project"`
	Secrets                int            `json:"secrets,omitempty" jsonschema:"the maximum number of secrets that can be created in the project"`
	ConfigMaps             int            `json:"configMaps,omitempty" jsonschema:"the maximum number of config maps that can be created in the project"`
	PersistentVolumeClaims int            `json:"persistentVolumeClaims,omitempty" jsonschema:"the maximum number of persistent volume claims that can be created in the project"`
	ServicesNodePorts      int            `json:"servicesNodePorts,omitempty" jsonschema:"the maximum number of services with node ports that can be created in the project"`
	ServicesLoadBalancers  int            `json:"servicesLoadBalancers,omitempty" jsonschema:"the maximum number of services with load balancers that can be created in the project"`
	RequestsCPU            int            `json:"requestsCpu,omitempty" jsonschema:"the amount of CPU resources (mCPUs) reserved for containers in the project"`
	RequestsMemory         int            `json:"requestsMemory,omitempty" jsonschema:"the amount of memory resources (MiB) reserved for containers in the project"`
	RequestsStorage        int            `json:"requestsStorage,omitempty" jsonschema:"the amount of storage resources (MiB) reserved for containers in the project"`
	LimitsCPU              int            `json:"limitsCpu,omitempty" jsonschema:"the maximum amount of CPU resources (mCPUs) that can be used by containers in the project"`
	LimitsMemory           int            `json:"limitsMemory,omitempty" jsonschema:"the maximum amount of memory resources (MiB) that can be used by containers in the project"`
	Extended               map[string]any `json:"extended,omitempty" jsonschema:"a map of any additional resource quotas to be applied to the project, where the key is the name of the resource quota and the value is the quantity (e.g., '10Gi' for storage)"`
}

type containerDefaultResourceLimit struct {
	CPULimit          int `json:"cpuLimit,omitempty" jsonschema:"the maximum amount of CPU resources (mCPUs) that can be used by containers in the project"`
	CPUReservation    int `json:"cpuReservation,omitempty" jsonschema:"the amount of CPU resources (mCPUs) reserved for containers in the project"`
	MemoryLimit       int `json:"memoryLimit,omitempty" jsonschema:"the maximum amount of memory resources (MiB) that can be used by containers in the project"`
	MemoryReservation int `json:"memoryReservation,omitempty" jsonschema:"the amount of memory resources (MiB) reserved for containers in the project"`
}

type createProjectParams struct {
	Cluster                       string                        `json:"cluster" jsonschema:"the cluster that the project belongs to"`
	Name                          string                        `json:"name" jsonschema:"the name of the project to be created"`
	Description                   string                        `json:"description,omitempty" jsonschema:"an optional description for the project"`
	DisplayName                   string                        `json:"displayName,omitempty" jsonschema:"an optional display name for the project"`
	ResourceQuota                 resourceQuota                 `json:"resourceQuota,omitempty" jsonschema:"optional resource quotas to be applied to the project"`
	NamespaceDefaultResourceQuota resourceQuota                 `json:"namespaceDefaultResourceQuota,omitempty" jsonschema:"optional default resource quotas to be applied to namespaces created within the project"`
	ContainerDefaultResourceLimit containerDefaultResourceLimit `json:"containerDefaultResourceLimit,omitempty" jsonschema:"optional default resource limits to be applied to containers created within the project"`
}

func (t *Tools) createProject(ctx context.Context, toolReq *mcp.CallToolRequest, params createProjectParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createProject called", zap.String("cluster", params.Cluster))

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
	if params.ContainerDefaultResourceLimit.CPULimit != 0 {
		containerResourceQuotas["limitsCpu"] = fmt.Sprintf("%dm", params.ContainerDefaultResourceLimit.CPULimit)
	}
	if params.ContainerDefaultResourceLimit.CPUReservation != 0 {
		containerResourceQuotas["requestsCpu"] = fmt.Sprintf("%dm", params.ContainerDefaultResourceLimit.CPUReservation)
	}
	if params.ContainerDefaultResourceLimit.MemoryLimit != 0 {
		containerResourceQuotas["limitsMemory"] = fmt.Sprintf("%dMi", params.ContainerDefaultResourceLimit.MemoryLimit)
	}
	if params.ContainerDefaultResourceLimit.MemoryReservation != 0 {
		containerResourceQuotas["requestsMemory"] = fmt.Sprintf("%dMi", params.ContainerDefaultResourceLimit.MemoryReservation)
	}

	if err := unstructured.SetNestedField(project.Object, containerResourceQuotas, "spec", "containerDefaultResourceLimit"); err != nil {
		return nil, fmt.Errorf("failed to set project container resource quotas: %w", err)
	}

	// Create the resource quota map if any quotas were specified
	quotaMap := buildResourceQuotaMap(params.ResourceQuota)
	if quotaMap["limit"] != nil {
		if err := unstructured.SetNestedField(project.Object, quotaMap, "spec", "resourceQuota"); err != nil {
			return nil, fmt.Errorf("failed to set project resource quotas: %w", err)
		}
	}

	// Create the namespaces default resource quota map if any quotas were specified
	namespaceResourceQuota := buildResourceQuotaMap(params.NamespaceDefaultResourceQuota)
	if namespaceResourceQuota["limit"] != nil {
		if err := unstructured.SetNestedField(project.Object, namespaceResourceQuota, "spec", "namespaceDefaultResourceQuota"); err != nil {
			return nil, fmt.Errorf("failed to set project namespace default resource quotas: %w", err)
		}
	}

	return project, nil
}

func buildResourceQuotaMap(quota resourceQuota) map[string]any {
	limitMap := make(map[string]any)
	if quota.Pods != 0 {
		limitMap["pods"] = strconv.Itoa(quota.Pods)
	}
	if quota.Services != 0 {
		limitMap["services"] = strconv.Itoa(quota.Services)
	}
	if quota.ReplicationControllers != 0 {
		limitMap["replicationControllers"] = strconv.Itoa(quota.ReplicationControllers)
	}
	if quota.Secrets != 0 {
		limitMap["secrets"] = strconv.Itoa(quota.Secrets)
	}
	if quota.ConfigMaps != 0 {
		limitMap["configMaps"] = strconv.Itoa(quota.ConfigMaps)
	}
	if quota.PersistentVolumeClaims != 0 {
		limitMap["persistentVolumeClaims"] = strconv.Itoa(quota.PersistentVolumeClaims)
	}
	if quota.ServicesNodePorts != 0 {
		limitMap["servicesNodePorts"] = strconv.Itoa(quota.ServicesNodePorts)
	}
	if quota.ServicesLoadBalancers != 0 {
		limitMap["servicesLoadBalancers"] = strconv.Itoa(quota.ServicesLoadBalancers)
	}
	if quota.RequestsCPU != 0 {
		limitMap["requestsCpu"] = fmt.Sprintf("%dm", quota.RequestsCPU)
	}
	if quota.RequestsMemory != 0 {
		limitMap["requestsMemory"] = fmt.Sprintf("%dMi", quota.RequestsMemory)
	}
	if quota.RequestsStorage != 0 {
		limitMap["requestsStorage"] = fmt.Sprintf("%dMi", quota.RequestsStorage)
	}
	if quota.LimitsCPU != 0 {
		limitMap["limitsCpu"] = fmt.Sprintf("%dm", quota.LimitsCPU)
	}
	if quota.LimitsMemory != 0 {
		limitMap["limitsMemory"] = fmt.Sprintf("%dMi", quota.LimitsMemory)
	}
	if quota.Extended != nil {
		limitMap["extended"] = quota.Extended
	}
	quotaMap := make(map[string]any)
	quotaMap["limit"] = limitMap
	return quotaMap
}
