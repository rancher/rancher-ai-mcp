package projects

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

var zapGetClusterResourceUsage = zap.String("tool", "getClusterResourceUsage")

const nodeRoleLabelPrefix = "node-role.kubernetes.io/"

type getClusterResourceUsageParams struct {
	Cluster string `json:"cluster" jsonschema:"the name of the cluster resource"`
}

// capacitySample holds the capacity, allocation and usage of a node or a whole cluster.
type capacitySample struct {
	cpuCapacity       *resource.Quantity
	cpuAllocatable    *resource.Quantity
	memoryCapacity    *resource.Quantity
	memoryAllocatable *resource.Quantity
	podsAllocatable   *resource.Quantity
	usage             sample
}

func newCapacitySample() capacitySample {
	return capacitySample{
		cpuCapacity:       resource.NewQuantity(0, resource.DecimalSI),
		cpuAllocatable:    resource.NewQuantity(0, resource.DecimalSI),
		memoryCapacity:    resource.NewQuantity(0, resource.BinarySI),
		memoryAllocatable: resource.NewQuantity(0, resource.BinarySI),
		podsAllocatable:   resource.NewQuantity(0, resource.DecimalSI),
		usage:             newSample(),
	}
}

func (c *capacitySample) add(other capacitySample) {
	c.cpuCapacity.Add(*other.cpuCapacity)
	c.cpuAllocatable.Add(*other.cpuAllocatable)
	c.memoryCapacity.Add(*other.memoryCapacity)
	c.memoryAllocatable.Add(*other.memoryAllocatable)
	c.podsAllocatable.Add(*other.podsAllocatable)
	c.usage.podCount += other.usage.podCount
	c.usage.cpuRequests.Add(*other.usage.cpuRequests)
	c.usage.cpuLimits.Add(*other.usage.cpuLimits)
	c.usage.memoryRequests.Add(*other.usage.memoryRequests)
	c.usage.memoryLimits.Add(*other.usage.memoryLimits)
	c.usage.cpuUsage.Add(*other.usage.cpuUsage)
	c.usage.memoryUsage.Add(*other.usage.memoryUsage)
}

// getClusterResourceUsage retrieves the resource capacity, allocation and usage for every node in a cluster, as well as cluster-wide totals.
func (t *Tools) getClusterResourceUsage(ctx context.Context, toolReq *mcp.CallToolRequest, params getClusterResourceUsageParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getClusterResourceUsage called")

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		zap.L().Error("failed to get cluster ID", zapGetClusterResourceUsage, zap.Error(err))
		return nil, nil, err
	}

	nodeResources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: clusterID,
		Kind:    "node",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get nodes", zapGetClusterResourceUsage, zap.Error(err))
		return nil, nil, err
	}

	podResources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: clusterID,
		Kind:    "pod",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get pods", zapGetClusterResourceUsage, zap.Error(err))
		return nil, nil, err
	}

	metricsAvailable := true
	// metricsUnavailableReason explains why actual usage is missing, so the LLM doesn't suggest installing
	// the metrics server when the user isn't allowed to read metrics.
	var metricsUnavailableReason string
	nodeMetricsResources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: clusterID,
		Kind:    "node.metrics.k8s.io",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		// Don't fail, as the metrics server might not be installed, or the user might not be allowed to read metrics.
		if apierrors.IsForbidden(err) {
			zap.L().Warn("not allowed to get node metrics, will skip actual usage data", zapGetClusterResourceUsage, zap.Error(err))
			metricsUnavailableReason = "the user is not allowed to read node metrics from the metrics.k8s.io API"
		} else {
			zap.L().Warn("failed to get node metrics, the metrics server might not be installed, will skip actual usage data", zapGetClusterResourceUsage, zap.Error(err))
			metricsUnavailableReason = fmt.Sprintf("node metrics could not be retrieved, the metrics server might not be installed or available: %v", err)
		}
		metricsAvailable = false
	}

	nodeMetrics := make(map[string]metricsv1beta1.NodeMetrics)
	for _, m := range nodeMetricsResources {
		var metrics metricsv1beta1.NodeMetrics
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m.Object, &metrics); err != nil {
			zap.L().Error("failed to convert unstructured object to NodeMetrics", zapGetClusterResourceUsage, zap.Error(err))
			return nil, nil, fmt.Errorf("failed to convert unstructured object to NodeMetrics: %w", err)
		}
		nodeMetrics[metrics.GetName()] = metrics
	}

	byNode := make(map[string]capacitySample)
	var nodes []corev1.Node
	for _, nodeResource := range nodeResources {
		var node corev1.Node
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(nodeResource.Object, &node); err != nil {
			zap.L().Error("failed to convert unstructured object to Node", zapGetClusterResourceUsage, zap.Error(err))
			return nil, nil, fmt.Errorf("failed to convert unstructured object to Node: %w", err)
		}
		nodes = append(nodes, node)

		s := newCapacitySample()
		s.cpuCapacity.Add(node.Status.Capacity[corev1.ResourceCPU])
		s.cpuAllocatable.Add(node.Status.Allocatable[corev1.ResourceCPU])
		s.memoryCapacity.Add(node.Status.Capacity[corev1.ResourceMemory])
		s.memoryAllocatable.Add(node.Status.Allocatable[corev1.ResourceMemory])
		s.podsAllocatable.Add(node.Status.Allocatable[corev1.ResourcePods])
		if m, ok := nodeMetrics[node.GetName()]; ok {
			s.usage.cpuUsage.Add(m.Usage[corev1.ResourceCPU])
			s.usage.memoryUsage.Add(m.Usage[corev1.ResourceMemory])
		}
		byNode[node.GetName()] = s
	}

	unscheduledPods := 0
	for _, podResource := range podResources {
		var pod corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(podResource.Object, &pod); err != nil {
			zap.L().Error("failed to convert unstructured object to Pod", zapGetClusterResourceUsage, zap.Error(err))
			return nil, nil, fmt.Errorf("failed to convert unstructured object to Pod: %w", err)
		}

		// Terminated pods no longer hold any resources on a node.
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		// Pending pods without a node could not be scheduled, which usually indicates the cluster is out of capacity.
		if pod.Spec.NodeName == "" {
			if pod.Status.Phase == corev1.PodPending {
				unscheduledPods++
			}
			continue
		}

		s, ok := byNode[pod.Spec.NodeName]
		if !ok {
			continue
		}

		// The scheduler reserves resources for every pod bound to a node, not only for running ones.
		cpuRequests, cpuLimits, memoryRequests, memoryLimits := podEffectiveResources(&pod)
		s.usage.podCount++
		s.usage.cpuRequests.Add(*cpuRequests)
		s.usage.cpuLimits.Add(*cpuLimits)
		s.usage.memoryRequests.Add(*memoryRequests)
		s.usage.memoryLimits.Add(*memoryLimits)
		byNode[pod.Spec.NodeName] = s
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].GetName() < nodes[j].GetName() })

	totals := newCapacitySample()
	readyNodes := 0
	nodeSummary := []map[string]any{}
	// Nodes can have no metrics even when the metrics server is available, e.g. nodes that
	// have just joined the cluster, or nodes that are not ready because their kubelet is unreachable.
	nodesWithoutMetrics := []string{}
	for _, node := range nodes {
		s := byNode[node.GetName()]
		totals.add(s)

		ready := isNodeReady(&node)
		if ready {
			readyNodes++
		}

		_, hasMetrics := nodeMetrics[node.GetName()]
		if metricsAvailable && !hasMetrics {
			nodesWithoutMetrics = append(nodesWithoutMetrics, node.GetName())
		}

		summary := toCapacitySummary(s, metricsAvailable && hasMetrics)
		summary["name"] = node.GetName()
		summary["roles"] = nodeRoles(&node)
		summary["ready"] = ready
		summary["unschedulable"] = node.Spec.Unschedulable
		nodeSummary = append(nodeSummary, summary)
	}

	// Cluster-wide usage is only reported if it covers every node, as it would otherwise be understated.
	clusterTotals := toCapacitySummary(totals, metricsAvailable && len(nodesWithoutMetrics) == 0)
	clusterTotals["nodeCount"] = len(nodes)
	clusterTotals["readyNodeCount"] = readyNodes
	clusterTotals["unscheduledPodCount"] = unscheduledPods

	usageSummary := map[string]any{
		"cluster":          clusterID,
		"metricsAvailable": metricsAvailable,
		"totals":           clusterTotals,
		"nodes":            nodeSummary,
	}
	if metricsUnavailableReason != "" {
		usageSummary["metricsUnavailableReason"] = metricsUnavailableReason
	}
	if len(nodesWithoutMetrics) > 0 {
		usageSummary["nodesWithoutMetrics"] = nodesWithoutMetrics
	}

	mcpResponse, err := response.CreateMcpResponseAny(usageSummary)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetClusterResourceUsage, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// toCapacitySummary converts a capacitySample into a map, including percentages relative to the allocatable resources.
// Actual usage is only included if metrics are available for the sample, to avoid reporting misleading zero values.
func toCapacitySummary(s capacitySample, metricsAvailable bool) map[string]any {
	cpu := map[string]any{
		"capacity":        formatCPU(s.cpuCapacity),
		"allocatable":     formatCPU(s.cpuAllocatable),
		"requests":        formatCPU(s.usage.cpuRequests),
		"limits":          formatCPU(s.usage.cpuLimits),
		"requestsPercent": percentOf(s.usage.cpuRequests.MilliValue(), s.cpuAllocatable.MilliValue()),
		"limitsPercent":   percentOf(s.usage.cpuLimits.MilliValue(), s.cpuAllocatable.MilliValue()),
	}
	memory := map[string]any{
		"capacity":        formatMemory(s.memoryCapacity),
		"allocatable":     formatMemory(s.memoryAllocatable),
		"requests":        formatMemory(s.usage.memoryRequests),
		"limits":          formatMemory(s.usage.memoryLimits),
		"requestsPercent": percentOf(s.usage.memoryRequests.Value(), s.memoryAllocatable.Value()),
		"limitsPercent":   percentOf(s.usage.memoryLimits.Value(), s.memoryAllocatable.Value()),
	}
	if metricsAvailable {
		cpu["usage"] = formatCPU(s.usage.cpuUsage)
		cpu["usagePercent"] = percentOf(s.usage.cpuUsage.MilliValue(), s.cpuAllocatable.MilliValue())
		memory["usage"] = formatMemory(s.usage.memoryUsage)
		memory["usagePercent"] = percentOf(s.usage.memoryUsage.Value(), s.memoryAllocatable.Value())
	}

	return map[string]any{
		"cpu":    cpu,
		"memory": memory,
		"pods": map[string]any{
			"allocatable": s.podsAllocatable.Value(),
			"count":       s.usage.podCount,
			"percent":     percentOf(int64(s.usage.podCount), s.podsAllocatable.Value()),
		},
	}
}

// formatCPU formats a CPU quantity in millicores, rounded up, or in whole cores if it is a whole number of cores.
// The metrics server reports usage in nanocores, so values like 1097324069n are displayed as 1098m instead.
func formatCPU(q *resource.Quantity) string {
	return resource.NewMilliQuantity(q.MilliValue(), resource.DecimalSI).String()
}

// formatMemory formats a memory quantity in the largest binary unit that keeps the value at least 1, rounded to one decimal place.
// Node memory is usually reported in Ki, so values like 61392032Ki are displayed as 58.5Gi instead.
func formatMemory(q *resource.Quantity) string {
	units := []string{"Ti", "Gi", "Mi", "Ki"}
	bytes := float64(q.Value())
	for i, unit := range units {
		value := math.Round(bytes/math.Pow(1024, float64(len(units)-i))*10) / 10
		if value >= 1 {
			return strconv.FormatFloat(value, 'f', -1, 64) + unit
		}
	}
	return strconv.FormatInt(q.Value(), 10)
}

// percentOf returns value as a percentage of total, rounded to one decimal place.
func percentOf(value, total int64) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(value)/float64(total)*1000) / 10
}

func isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// nodeRoles returns the roles of a node, based on the node-role.kubernetes.io/<role> labels.
func nodeRoles(node *corev1.Node) []string {
	roles := []string{}
	for label := range node.GetLabels() {
		if role, ok := strings.CutPrefix(label, nodeRoleLabelPrefix); ok && role != "" {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return roles
}
