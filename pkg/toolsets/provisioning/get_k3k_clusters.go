package provisioning

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type getK3kClustersParams struct {
	Clusters []string `json:"clusters" jsonschema:"list of clusters to get virtual clusters from. Empty to return virtual clusters for all clusters"`
}

type K3kClusterDetails struct {
	Name   string                 `json:"name"`
	Spec   map[string]interface{} `json:"spec,omitempty"`
	Status map[string]interface{} `json:"status,omitempty"`
}

// getK3kClusters retrieves a list of K3k clusters deployed across specified downstream clusters.
// If no clusters are provided, it fetches K3k clusters from all available downstream clusters.
// Returns a JSON map of downstream cluster names to lists of K3k cluster names.
func (t *Tools) getK3kClusters(ctx context.Context, toolReq *mcp.CallToolRequest, params getK3kClustersParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getK3kClusters called for clusters ", zap.Any("clusters", params.Clusters))

	var clusters []string
	if len(params.Clusters) == 0 {
		clusterList, err := t.client.GetResources(ctx, client.ListParams{
			Cluster: "local",
			Kind:    "managementcluster",
			URL:     toolReq.Extra.Header.Get(urlHeader),
			Token:   middleware.Token(ctx),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get clusters: %w", err)
		}
		for _, cluster := range clusterList {
			clusters = append(clusters, cluster.GetName())
		}
	} else {
		clusters = params.Clusters
	}
	k3kClustersMap := map[string][]K3kClusterDetails{}

	for _, cluster := range clusters {
		k3kClusters, err := t.client.GetResources(ctx, client.ListParams{
			Cluster: cluster,
			Kind:    "k3kcluster",
			URL:     toolReq.Extra.Header.Get(urlHeader),
			Token:   middleware.Token(ctx),
		})

		if err != nil {
			zap.L().Warn("failed to get k3k clusters", zap.String("tool", "getK3kClusters"), zap.String("Downstream cluster", cluster))
		} else {
			var clusterDetails []K3kClusterDetails
			for _, k3kCluster := range k3kClusters {
				spec, _, _ := unstructured.NestedMap(k3kCluster.Object, "spec")
				status, _, _ := unstructured.NestedMap(k3kCluster.Object, "status")
				clusterDetails = append(clusterDetails, K3kClusterDetails{
					Name:   k3kCluster.GetName(),
					Spec:   spec,
					Status: status,
				})
			}
			k3kClustersMap[cluster] = clusterDetails
		}
	}

	response, err := json.Marshal(k3kClustersMap)
	if err != nil {
		zap.L().Error("failed to create response", zap.String("tool", "getK3kClusters"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(response)}},
	}, nil, nil
}
