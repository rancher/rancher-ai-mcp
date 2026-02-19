package provisioning

import (
	"context"
	"fmt"
	"mcp/internal/middleware"
	"mcp/pkg/converter"
	"mcp/pkg/response"
	"mcp/pkg/utils"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

type CreateImportedClusterParams struct {
	ClusterName              string `json:"clusterName" jsonschema:"The name of the cluster to create"`
	ClusterDescription       string `json:"clusterDescription" jsonschema:"Optional description for the cluster"`
	VersionManagementSetting string `json:"VersionManagementSetting" jsonschema:"Enable version management for the cluster. If not specified the global setting will be used. Potential values are 'system-default', 'true', and 'false'."`
}

func (t *Tools) CreateImportedCluster(ctx context.Context, toolReq *mcp.CallToolRequest, params CreateImportedClusterParams) (*mcp.CallToolResult, any, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"clusterName":              params.ClusterName,
		"clusterDescription":       params.ClusterDescription,
		"versionManagementSetting": params.VersionManagementSetting,
	})

	log.Debug("Creating imported cluster")

	if params.VersionManagementSetting != "" && (params.VersionManagementSetting != "true" && params.VersionManagementSetting != "false" && params.VersionManagementSetting != "system-default") {
		return nil, nil, fmt.Errorf("invalid value for VersionManagementSetting: %s. Valid values are 'system-default', 'true', and 'false'", params.VersionManagementSetting)
	}

	if params.VersionManagementSetting == "" {
		params.VersionManagementSetting = "system-default"
	}

	if params.ClusterName == "" {
		return nil, nil, fmt.Errorf("ClusterName is required")
	}

	cluster := &unstructured.Unstructured{
		Object: make(map[string]interface{}),
	}
	cluster.SetKind("Cluster")
	cluster.SetAPIVersion(converter.ManagementGroup + "/v3")
	// This mimics behavior built within the Norman API, which is typically how
	// custom clusters are created. The format of 'c-xxxxx' is important, as Rancher
	// uses it to determine the cluster type.
	cluster.SetName(fmt.Sprintf("c-%s", utilrand.String(5)))
	cluster.SetAnnotations(map[string]string{
		"rancher.io/imported-cluster-version-management": params.VersionManagementSetting,
		"generate-name": "c-",
	})

	if err := unstructured.SetNestedField(cluster.Object, true, "spec", "imported"); err != nil {
		return nil, nil, fmt.Errorf("failed to create cluster object: %w", err)
	}

	if err := unstructured.SetNestedField(cluster.Object, params.ClusterName, "spec", "displayName"); err != nil {
		return nil, nil, fmt.Errorf("failed to create cluster object: %w", err)
	}

	if err := unstructured.SetNestedField(cluster.Object, params.ClusterDescription, "spec", "description"); err != nil {
		return nil, nil, fmt.Errorf("failed to create cluster object: %w", err)
	}

	// Create the cluster
	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), toolReq.Extra.Header.Get(urlHeader), "", LocalCluster, converter.K8sKindsToGVRs[converter.ManagementClusterResourceKind])
	if err != nil {
		return nil, nil, err
	}

	createdCluster, err := resourceInterface.Create(ctx, cluster, metav1.CreateOptions{})
	if err != nil {
		log.Error(fmt.Sprintf("failed to create imported cluster: %v", err))
		return nil, nil, fmt.Errorf("failed to create imported cluster %s: %w", params.ClusterName, err)
	}

	log.Debug("Successfully created imported cluster")

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{createdCluster}, LocalCluster)
	if err != nil {
		log.Error(fmt.Sprintf("failed to create mcp response: %v", err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
