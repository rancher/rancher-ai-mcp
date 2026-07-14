package core

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

const (
	podLogsTailLines int64 = 50
)

// containerLogs holds logs for multiple containers.
type containerLogs struct {
	Logs map[string]any `json:"logs"`
}

// inspectPod retrieves detailed information about a specific pod, its owner, metrics, and logs.
func (t *Tools) inspectPod(ctx context.Context, toolReq *mcp.CallToolRequest, params specificResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("inspectPod called")

	podResource, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   params.Cluster,
		Kind:      "pod",
		Namespace: params.Namespace,
		Name:      params.Name,
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get Pod", zap.String("tool", "inspectPod"), zap.Error(err))
		return nil, nil, err
	}

	var pod corev1.Pod
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(podResource.Object, &pod); err != nil {
		zap.L().Error("failed to convert unstructured object to Pod", zap.String("tool", "inspectPod"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to convert unstructured object to Pod: %w", err)
	}

	// find the parent of the pod
	var replicaSetName string
	for _, or := range pod.OwnerReferences {
		if or.Kind == "ReplicaSet" {
			replicaSetName = or.Name
			break
		}
	}

	var parentResource *unstructured.Unstructured

	// ReplicaSet may not exist
	if replicaSetName != "" {
		replicaSetResource, err := t.client.GetResource(ctx, client.GetParams{
			Cluster:   params.Cluster,
			Kind:      "replicaset",
			Namespace: params.Namespace,
			Name:      replicaSetName,
			Token:     middleware.Token(ctx),
		})
		if err != nil {
			zap.L().Error("failed to get ReplicaSet", zap.String("tool", "inspectPod"), zap.Error(err))
			return nil, nil, err
		}

		var replicaSet appsv1.ReplicaSet
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(replicaSetResource.Object, &replicaSet); err != nil {
			zap.L().Error("failed to convert unstructured object to ReplicaSet", zap.String("tool", "inspectPod"), zap.Error(err))
			return nil, nil, fmt.Errorf("failed to convert unstructured object to Pod: %w", err)
		}

		var parentName, parentKind string
		for _, or := range replicaSet.OwnerReferences {
			if or.Kind == "Deployment" || or.Kind == "StatefulSet" || or.Kind == "DaemonSet" {
				parentName = or.Name
				parentKind = or.Kind
				break
			}
		}

		if parentName != "" {
			var err error
			parentResource, err = t.client.GetResource(ctx, client.GetParams{
				Cluster:   params.Cluster,
				Kind:      parentKind,
				Namespace: params.Namespace,
				Name:      parentName,
				Token:     middleware.Token(ctx),
			})
			if err != nil {
				zap.L().Error("failed to get parent resource", zap.String("tool", "inspectPod"), zap.Error(err))
				return nil, nil, err
			}
		}
	}

	// ignore error as Metrics Server might not be installed in the cluster
	podMetrics, _ := t.client.GetResource(ctx, client.GetParams{
		Cluster:   params.Cluster,
		Kind:      "pod.metrics.k8s.io",
		Namespace: params.Namespace,
		Name:      params.Name,
		Token:     middleware.Token(ctx),
	})

	logs, err := t.getPodLogs(ctx, params.Cluster, middleware.Token(ctx), pod)
	if err != nil {
		zap.L().Error("failed to get pod logs", zap.String("tool", "inspectPod"), zap.Error(err))
		return nil, nil, err
	}

	resources := []*unstructured.Unstructured{podResource, logs}
	if parentResource != nil {
		resources = append(resources, parentResource)
	}
	if podMetrics != nil {
		resources = append(resources, podMetrics)
	}

	mcpResponse, err := response.CreateMcpResponse(resources, params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "inspectPod"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// getPodLogs retrieves the logs for all containers in a pod.
// It returns the logs as an unstructured object with container names as keys.
// Only the last 50 lines of logs are retrieved per container to limit payload size.
func (t *Tools) getPodLogs(ctx context.Context, cluster string, token string, pod corev1.Pod) (*unstructured.Unstructured, error) {
	clientset, err := t.client.CreateClientSet(ctx, token, cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}
	logs := containerLogs{
		Logs: make(map[string]any),
	}
	for _, container := range pod.Spec.Containers {
		podLogOptions := corev1.PodLogOptions{
			TailLines: ptr.To(podLogsTailLines),
			Container: container.Name,
		}
		req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &podLogOptions)
		podLogs, err := req.Stream(ctx)
		if err != nil {
			// The container may not exist or may have terminated, so we log the error and continue with other containers instead of failing the entire function.
			zap.L().Warn("unable to retrieve logs for container",
				zap.String("container", container.Name),
				zap.String("pod", pod.Name),
				zap.Error(err))
			logs.Logs[container.Name] = fmt.Sprintf("unable to retrieve logs: %v", err)
			continue
		}
		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, podLogs)
		if err != nil {
			zap.L().Warn("failed to copy log stream",
				zap.String("container", container.Name),
				zap.String("pod", pod.Name),
				zap.Error(err))
			logs.Logs[container.Name] = fmt.Sprintf("failed to read logs: %v", err)
			if err := podLogs.Close(); err != nil {
				zap.L().Warn("failed to close pod logs stream", zap.Error(err))
			}
			continue
		}
		logs.Logs[container.Name] = buf.String()
		if err := podLogs.Close(); err != nil {
			zap.L().Warn("failed to close pod logs stream", zap.Error(err))
		}
	}

	return &unstructured.Unstructured{Object: map[string]any{"pod-logs": logs.Logs}}, nil
}
