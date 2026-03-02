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

var zapGetResourceUsage = zap.String("tool", "getResourceUsage")

type getResourceUsageParams struct {
	Cluster   string `json:"cluster" jsonschema:"the name of the cluster resource"`
	Project   string `json:"project,omitempty" jsonschema:"(optional) the name of the project resource"`
	Namespace string `json:"namespace,omitempty" jsonschema:"(optional) the namespace to query for resource usage"`
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

// getResourceUsage retrieves the resource usage for a namespace, project or all projects in a cluster.
func (t *Tools) getResourceUsage(ctx context.Context, toolReq *mcp.CallToolRequest, params getResourceUsageParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getResourceUsage called")

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), t.rancherURL(toolReq), params.Cluster)
	if err != nil {
		zap.L().Error("failed to get cluster ID", zapGetResourceUsage, zap.Error(err))
		return nil, nil, err
	}

	usageSummary := map[string]any{
		"cluster": clusterID,
	}

	if params.Namespace != "" {
		ns, err := t.client.GetResource(ctx, client.GetParams{
			Cluster: clusterID,
			Kind:    "namespace",
			Name:    params.Namespace,
			URL:     t.rancherURL(toolReq),
			Token:   middleware.Token(ctx),
		})
		if err != nil {
			zap.L().Error("failed to get namespace", zapGetResourceUsage, zap.String("namespace", params.Namespace), zap.Error(err))
			return nil, nil, err
		}

		nsTotals, err := t.getNamespaceResourceUsage(ctx, toolReq, clusterID, ns.GetName())
		if err != nil {
			zap.L().Error("failed to get resource usage for namespace", zapGetResourceUsage, zap.String("namespace", params.Namespace), zap.Error(err))
			return nil, nil, err
		}

		usageSummary["namespace"] = toNamespaceSummary(params.Namespace, nsTotals)
	} else {

		var projectResources []*unstructured.Unstructured
		if params.Project != "" {
			projectID, err := t.getProjectID(ctx, middleware.Token(ctx), t.rancherURL(toolReq), clusterID, params.Project)
			if err != nil {
				zap.L().Error("failed to get project ID", zapGetResourceUsage, zap.Error(err))
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
				zap.L().Error("failed to get project", zapGetResourceUsage, zap.Error(err))
				return nil, nil, err
			}
			projectResources = []*unstructured.Unstructured{projectResource}
		} else {
			projectResources, err = t.client.GetResources(ctx, client.ListParams{
				Cluster:   LocalCluster,
				Kind:      "project",
				Namespace: clusterID,
				URL:       t.rancherURL(toolReq),
				Token:     middleware.Token(ctx),
			})
			if err != nil {
				zap.L().Error("failed to list projects", zapGetResourceUsage, zap.Error(err))
				return nil, nil, err
			}
		}

		var projectSummary []map[string]any
		for _, projectResource := range projectResources {
			projectDisplayName, _, err := unstructured.NestedString(projectResource.Object, "spec", "displayName")
			if err != nil {
				zap.L().Error("failed to get displayName from project", zapGetResourceUsage, zap.Error(err))
				return nil, nil, err
			}

			projectLabel, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
				MatchLabels: map[string]string{
					"field.cattle.io/projectId": projectResource.GetName(),
				},
			})
			if err != nil {
				zap.L().Error("failed to create label selector", zapGetResourceUsage, zap.Error(err))
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
				zap.L().Error("failed to get namespaces for project", zapGetResourceUsage, zap.Error(err))
				return nil, nil, err
			}

			totals := newSample()
			byNs := make(map[string]sample)

			// aggregate resource usage across all namespaces in the project
			for _, ns := range projectNamespaces {
				nsTotals, err := t.getNamespaceResourceUsage(ctx, toolReq, clusterID, ns.GetName())
				if err != nil {
					zap.L().Error("failed to get resource usage for namespace", zapGetResourceUsage, zap.String("namespace", ns.GetName()), zap.Error(err))
					return nil, nil, err
				}

				totals.podCount += nsTotals.podCount
				totals.cpuRequests.Add(*nsTotals.cpuRequests)
				totals.cpuLimits.Add(*nsTotals.cpuLimits)
				totals.memoryRequests.Add(*nsTotals.memoryRequests)
				totals.memoryLimits.Add(*nsTotals.memoryLimits)
				totals.cpuUsage.Add(*nsTotals.cpuUsage)
				totals.memoryUsage.Add(*nsTotals.memoryUsage)

				byNs[ns.GetName()] = nsTotals
			}

			var namespaceSummary []map[string]any
			for ns, nsTotals := range byNs {
				namespaceSummary = append(namespaceSummary, toNamespaceSummary(ns, nsTotals))
			}

			projectSummary = append(projectSummary, map[string]any{
				"name":        projectResource.GetName(),
				"displayName": projectDisplayName,
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
				"namespaces": namespaceSummary,
			})
		}

		usageSummary["projects"] = projectSummary
	}

	mcpResponse, err := response.CreateMcpResponseAny(usageSummary)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetResourceUsage, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

func (t *Tools) getNamespaceResourceUsage(ctx context.Context, toolReq *mcp.CallToolRequest, clusterID, namespace string) (sample, error) {
	empty := newSample()

	// Fetch pods.
	podResources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   clusterID,
		Kind:      "pod",
		Namespace: namespace,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		return empty, fmt.Errorf("failed to get pods for namespace %s: %w", namespace, err)
	}

	// Fetch pod metrics.
	metricsResources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   clusterID,
		Kind:      "pod.metrics.k8s.io",
		Namespace: namespace,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		// Log warning but don't fail - metrics server might not be installed
		zap.L().Warn("failed to get pod metrics, will skip actual usage data", zapGetResourceUsage, zap.String("namespace", namespace), zap.Error(err))
	}

	// Metrics lookup map.
	metricsByPod := make(map[string]*unstructured.Unstructured)
	for _, m := range metricsResources {
		metricsByPod[m.GetName()] = m
	}

	totals := newSample()
	for _, podResource := range podResources {
		var pod corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(podResource.Object, &pod); err != nil {
			return empty, fmt.Errorf("failed to convert unstructured object to Pod: %w", err)
		}

		// Skip pods that are not running or succeeded.
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodSucceeded {
			continue
		}

		totals.podCount++

		// The pod's effective request/limit for a resource is calulated as follows:
		// effective = max(sum(app containers), max(init containers))
		// See https://kubernetes.io/docs/concepts/workloads/pods/init-containers/#resource-sharing-within-containers

		// Sum of all app containers request/limit for resources.
		appCPURequests := resource.NewQuantity(0, resource.DecimalSI)
		appCPULimits := resource.NewQuantity(0, resource.DecimalSI)
		appMemoryRequests := resource.NewQuantity(0, resource.BinarySI)
		appMemoryLimits := resource.NewQuantity(0, resource.BinarySI)

		for _, container := range pod.Spec.Containers {
			if cpuReq, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
				appCPURequests.Add(cpuReq)
			}
			if cpuLimit, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
				appCPULimits.Add(cpuLimit)
			}
			if memReq, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
				appMemoryRequests.Add(memReq)
			}
			if memLimit, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
				appMemoryLimits.Add(memLimit)
			}
		}

		// The effective (max) init request/limit for resources.
		initCPURequests := resource.NewQuantity(0, resource.DecimalSI)
		initCPULimits := resource.NewQuantity(0, resource.DecimalSI)
		initMemoryRequests := resource.NewQuantity(0, resource.BinarySI)
		initMemoryLimits := resource.NewQuantity(0, resource.BinarySI)

		for _, container := range pod.Spec.InitContainers {
			if cpuReq, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
				initCPURequests = quantityMax(initCPURequests, &cpuReq)
			}
			if cpuLimit, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
				initCPULimits = quantityMax(initCPULimits, &cpuLimit)
			}
			if memReq, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
				initMemoryRequests = quantityMax(initMemoryRequests, &memReq)
			}
			if memLimit, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
				initMemoryLimits = quantityMax(initMemoryLimits, &memLimit)
			}
		}

		// Pod's effective request/limit = max(sum(app containers), max(init containers)).
		totals.cpuRequests.Add(*quantityMax(appCPURequests, initCPURequests))
		totals.cpuLimits.Add(*quantityMax(appCPULimits, initCPULimits))
		totals.memoryRequests.Add(*quantityMax(appMemoryRequests, initMemoryRequests))
		totals.memoryLimits.Add(*quantityMax(appMemoryLimits, initMemoryLimits))

		if m, hasMetrics := metricsByPod[pod.GetName()]; hasMetrics {
			var podMetrics metricsv1beta1.PodMetrics
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m.Object, &podMetrics); err != nil {
				return empty, fmt.Errorf("failed to convert unstructured object to PodMetrics: %w", err)
			}

			for _, container := range podMetrics.Containers {
				if cpu, ok := container.Usage[corev1.ResourceCPU]; ok {
					totals.cpuUsage.Add(cpu)
				}
				if mem, ok := container.Usage[corev1.ResourceMemory]; ok {
					totals.memoryUsage.Add(mem)
				}
			}
		}
	}

	return totals, nil
}

// quantityMax returns a pointer to the larger of two resource quantities.
func quantityMax(a, b *resource.Quantity) *resource.Quantity {
	if a.Cmp(*b) >= 0 {
		return a
	}
	return b
}

func toNamespaceSummary(name string, s sample) map[string]any {
	return map[string]any{
		"namespace": name,
		"totals": map[string]any{
			"podCount": s.podCount,
			"cpu": map[string]any{
				"requests": s.cpuRequests.String(),
				"limits":   s.cpuLimits.String(),
				"usage":    s.cpuUsage.String(),
			},
			"memory": map[string]any{
				"requests": s.memoryRequests.String(),
				"limits":   s.memoryLimits.String(),
				"usage":    s.memoryUsage.String(),
			},
		},
	}
}
