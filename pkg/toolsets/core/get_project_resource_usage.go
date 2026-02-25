package core

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

var zapToolName = zap.String("tool", "getProjectResourceUsage")

type getProjectResourceUsageParams struct {
	Name    string `json:"name" jsonschema:"the name of the project resource"`
	Cluster string `json:"cluster" jsonschema:"the name of the cluster resource the project belongs to"`
}

type sample struct {
	cpuRequests    *resource.Quantity
	cpuLimits      *resource.Quantity
	memoryRequests *resource.Quantity
	memoryLimits   *resource.Quantity
	cpuUsage       *resource.Quantity
	memoryUsage    *resource.Quantity
	podCount       int
}

func newSample() sample {
	return sample{
		cpuRequests:    resource.NewQuantity(0, resource.DecimalSI),
		cpuLimits:      resource.NewQuantity(0, resource.DecimalSI),
		memoryRequests: resource.NewQuantity(0, resource.BinarySI),
		memoryLimits:   resource.NewQuantity(0, resource.BinarySI),
		cpuUsage:       resource.NewQuantity(0, resource.DecimalSI),
		memoryUsage:    resource.NewQuantity(0, resource.BinarySI),
	}
}

// getProjectResourceUsage retrieves the resource usage for a specific project.
func (t *Tools) getProjectResourceUsage(ctx context.Context, toolReq *mcp.CallToolRequest, params getProjectResourceUsageParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getProjectResourceUsage called")

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), t.rancherURL(toolReq), params.Cluster)
	if err != nil {
		zap.L().Error("failed to get cluster ID", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectID, err := t.getProjectID(ctx, middleware.Token(ctx), t.rancherURL(toolReq), clusterID, params.Name)
	if err != nil {
		zap.L().Error("failed to get project ID", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectResource, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   LocalCluster,
		Kind:      "project",
		Namespace: clusterID,
		Name:      projectID,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get project", zapToolName, zap.Error(err))
		return nil, nil, err
	}

	projectDisplayName, _, err := unstructured.NestedString(projectResource.Object, "spec", "displayName")
	if err != nil {
		zap.L().Error("failed to get displayName from project", zapToolName, zap.Error(err))
		return nil, nil, err
	}

	projectLabel, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{
			"field.cattle.io/projectId": projectID,
		},
	})
	if err != nil {
		zap.L().Error("failed to create label selector", zapToolName, zap.Error(err))
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
		zap.L().Error("failed to get namespaces for project", zapToolName, zap.Error(err))
		return nil, nil, err
	}

	totals := newSample()
	byNs := make(map[string]sample)

	// aggregate resource usage across all namespaces in the project
	for _, ns := range projectNamespaces {
		// Fetch pods.
		podResources, err := t.client.GetResources(ctx, client.ListParams{
			Cluster:   clusterID,
			Kind:      "pod",
			Namespace: ns.GetName(),
			URL:       t.rancherURL(toolReq),
			Token:     middleware.Token(ctx),
		})
		if err != nil {
			zap.L().Error("failed to get pods for namespace", zapToolName, zap.String("namespace", ns.GetName()), zap.Error(err))
			return nil, nil, err
		}

		// Fetch pod metrics.
		metricsResources, err := t.client.GetResources(ctx, client.ListParams{
			Cluster:   clusterID,
			Kind:      "pod.metrics.k8s.io",
			Namespace: ns.GetName(),
			URL:       t.rancherURL(toolReq),
			Token:     middleware.Token(ctx),
		})
		if err != nil {
			// Log warning but don't fail - metrics server might not be installed
			zap.L().Warn("failed to get pod metrics, will skip actual usage data", zapToolName, zap.String("namespace", ns.GetName()), zap.Error(err))
		}

		// Metrics lookup map.
		metricsByPod := make(map[string]*unstructured.Unstructured)
		for _, m := range metricsResources {
			metricsByPod[m.GetName()] = m
		}

		nsTotals := newSample()

		for _, podResource := range podResources {
			var pod corev1.Pod
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(podResource.Object, &pod); err != nil {
				zap.L().Error("failed to convert unstructured object to Pod", zapToolName, zap.Error(err))
				return nil, nil, fmt.Errorf("failed to convert unstructured object to Pod: %w", err)
			}

			// Skip pods that are not running or succeeded
			if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodSucceeded {
				continue
			}

			totals.podCount++
			nsTotals.podCount++

			// Aggregate resources from all containers
			for _, container := range pod.Spec.Containers {
				if cpuReq, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
					totals.cpuRequests.Add(cpuReq)
					nsTotals.cpuRequests.Add(cpuReq)
				}
				if cpuLimit, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
					totals.cpuLimits.Add(cpuLimit)
					nsTotals.cpuLimits.Add(cpuLimit)
				}
				if memReq, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
					totals.memoryRequests.Add(memReq)
					nsTotals.memoryRequests.Add(memReq)
				}
				if memLimit, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
					totals.memoryLimits.Add(memLimit)
					nsTotals.memoryLimits.Add(memLimit)
				}
			}

			// Aggregate resources from init containers
			for _, container := range pod.Spec.InitContainers {
				if cpuReq, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
					totals.cpuRequests.Add(cpuReq)
					nsTotals.cpuRequests.Add(cpuReq)
				}
				if cpuLimit, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
					totals.cpuLimits.Add(cpuLimit)
					nsTotals.cpuLimits.Add(cpuLimit)
				}
				if memReq, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
					totals.memoryRequests.Add(memReq)
					nsTotals.memoryRequests.Add(memReq)
				}
				if memLimit, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
					totals.memoryLimits.Add(memLimit)
					nsTotals.memoryLimits.Add(memLimit)
				}
			}

			if m, hasMetrics := metricsByPod[pod.GetName()]; hasMetrics {
				var podMetrics metricsv1beta1.PodMetrics
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m.Object, &podMetrics); err != nil {
					zap.L().Error("failed to convert unstructured object to PodMetrics", zapToolName, zap.Error(err))
					return nil, nil, fmt.Errorf("failed to convert unstructured object to PodMetrics: %w", err)
				}

				for _, container := range podMetrics.Containers {
					if cpu, ok := container.Usage[corev1.ResourceCPU]; ok {
						totals.cpuUsage.Add(cpu)
						nsTotals.cpuUsage.Add(cpu)
					}
					if mem, ok := container.Usage[corev1.ResourceMemory]; ok {
						totals.memoryUsage.Add(mem)
						nsTotals.memoryUsage.Add(mem)
					}
				}
			}
		}
		byNs[ns.GetName()] = nsTotals
	}

	// Create a resource usage summary object
	namespaceSummary := make(map[string]any)
	for ns, nsTotals := range byNs {
		namespaceSummary[ns] = map[string]any{
			"namespace": ns,
			"cluster":   clusterID,
			"totals": map[string]any{
				"podCount": nsTotals.podCount,
				"cpu": map[string]any{
					"requests": nsTotals.cpuRequests.String(),
					"limits":   nsTotals.cpuLimits.String(),
					"usage":    nsTotals.cpuUsage.String(),
				},
				"memory": map[string]any{
					"requests": nsTotals.memoryRequests.String(),
					"limits":   nsTotals.memoryLimits.String(),
					"usage":    nsTotals.memoryUsage.String(),
				},
			},
		}
	}

	resourceUsageSummary := map[string]any{
		"projectResourceUsageSummary": map[string]any{
			"project": map[string]any{
				"name":        projectID,
				"displayName": projectDisplayName,
				"cluster":     clusterID,
				"totals": map[string]any{
					"podCount": totals.podCount,
					"cpu": map[string]any{
						"requests": totals.cpuRequests.String(),
						"limits":   totals.cpuLimits.String(),
						"usage":    totals.cpuUsage.String(),
					},
					"memory": map[string]any{
						"requests": totals.memoryRequests.String(),
						"limits":   totals.memoryLimits.String(),
						"usage":    totals.memoryUsage.String(),
					},
				},
			},
			"namespaces": namespaceSummary,
		},
	}

	mcpResponse, err := response.CreateMcpResponseAny(resourceUsageSummary)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapToolName, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
