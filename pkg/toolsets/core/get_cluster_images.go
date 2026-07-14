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
	Clusters []string `json:"clusters,omitempty" jsonschema:"list of clusters to get images from. Empty to return images for all clusters"`
}

type podReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type imageUsage struct {
	Image string         `json:"image"`
	Pods  []podReference `json:"pods"`
}

// getClusterImages retrieves all container images used across specified clusters,
// along with the pods (name and namespace) using each image.
// If no clusters are provided, it fetches images from all available clusters.
// Returns a JSON map of cluster names to lists of image usage entries.
func (t *Tools) getClusterImages(ctx context.Context, toolReq *mcp.CallToolRequest, params getClusterImagesParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getClusterImages called")

	var clusters []string
	if len(params.Clusters) == 0 {
		clusterList, err := t.client.GetResources(ctx, client.ListParams{
			Cluster: "local",
			Kind:    "managementcluster",
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

	imagesInClusters := map[string][]imageUsage{}

	for _, cluster := range clusters {
		imageIndex := map[string]int{} // image name → index in slice
		var usages []imageUsage

		unstructuredPods, err := t.client.GetResources(ctx, client.ListParams{
			Cluster: cluster,
			Kind:    "pod",
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
			ref := podReference{Name: pod.Name, Namespace: pod.Namespace}
			var containerImages []string
			for _, c := range pod.Spec.InitContainers {
				containerImages = append(containerImages, c.Image)
			}
			for _, c := range pod.Spec.Containers {
				containerImages = append(containerImages, c.Image)
			}
			for _, image := range containerImages {
				if idx, ok := imageIndex[image]; ok {
					usages[idx].Pods = append(usages[idx].Pods, ref)
				} else {
					imageIndex[image] = len(usages)
					usages = append(usages, imageUsage{Image: image, Pods: []podReference{ref}})
				}
			}
		}

		if usages == nil {
			usages = []imageUsage{}
		}
		imagesInClusters[cluster] = usages
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
