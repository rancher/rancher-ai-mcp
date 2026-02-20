package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type getClusterImagesParams struct {
	Clusters []string `json:"clusters" jsonschema:"the clusters where images are returned"`
}

// getClusterImages retrieves all container images used across specified clusters.
// If no clusters are provided, it fetches images from all available clusters.
// Returns a JSON map of cluster names to lists of container images.
func (t *Tools) getClusterImages(ctx context.Context, toolReq *mcp.CallToolRequest, params getClusterImagesParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getClusterImages called")

	var clusters []string
	if len(params.Clusters) == 0 {
		clusterList, err := t.client.GetResources(ctx, client.ListParams{
			Cluster: "local",
			Kind:    "managementcluster",
			URL:     t.rancherURL(toolReq),
			Token:   middleware.Token(ctx),
		})

		if err != nil {
			zap.L().Error("failed to get clusters", zap.String("tool", "getClusterImages"), zap.Error(err))
			return nil, nil, fmt.Errorf("failed to get clusters: %w", err)
		}
		for _, cluster := range clusterList {
			clusters = append(clusters, cluster.GetName())
		}
	} else {
		clusters = params.Clusters
	}

	imagesInClusters := map[string][]string{}

	for _, cluster := range clusters {
		images := []string{}
		unstructuredPods, err := t.client.GetResources(ctx, client.ListParams{
			Cluster: cluster,
			Kind:    "pod",
			URL:     t.rancherURL(toolReq),
			Token:   middleware.Token(ctx),
		})
		if err != nil {
			zap.L().Error("failed to get pods", zap.String("tool", "getClusterImages"), zap.Error(err))
			return nil, nil, fmt.Errorf("failed to get pods: %w", err)
		}
		for _, unstructuredPod := range unstructuredPods {
			var pod corev1.Pod
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredPod.Object, &pod); err != nil {
				zap.L().Error("failed convert unstructured object to Pod", zap.String("tool", "getClusterImages"), zap.Error(err))
				return nil, nil, fmt.Errorf("failed to convert unstructured object to Pod: %w", err)
			}
			for _, container := range pod.Spec.InitContainers {
				images = append(images, container.Image)
			}
			for _, container := range pod.Spec.Containers {
				images = append(images, container.Image)
			}
		}

		imagesInClusters[cluster] = images
	}

	response, err := json.Marshal(imagesInClusters)
	if err != nil {
		zap.L().Error("failed to create response", zap.String("tool", "getClusterImages"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(response)}},
	}, nil, nil

}
