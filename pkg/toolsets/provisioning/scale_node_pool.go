package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"mcp/internal/middleware"
	"mcp/pkg/converter"
	"mcp/pkg/response"
	"mcp/pkg/utils"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ScaleNodePoolParameters struct {
	Cluster          string `json:"cluster" jsonschema:"the name of the provisioning cluster"`
	Namespace        string `json:"namespace" jsonschema:"the namespace of the resource"`
	NodePoolName     string `json:"nodePoolName" jsonschema:"the name of the node pool to scale"`
	DesiredSize      int    `json:"desiredSize" jsonschema:"the desired size of the node pool"`
	AmountToAdd      int    `json:"amountToAdd" jsonschema:"the amount of nodes to add to the node pool. if specified, DesiredSize will be ignored"`
	AmountToSubtract int    `json:"AmountToSubtract" jsonschema:"the amount of nodes to remove from the node pool. if specified, DesiredSize will be ignored"`
}

func (t *Tools) ScaleClusterNodePool(ctx context.Context, toolReq *mcp.CallToolRequest, params ScaleNodePoolParameters) (*mcp.CallToolResult, any, error) {
	if params.Namespace == "" || params.Namespace == "default" {
		params.Namespace = DefaultClusterResourcesNamespace
	}

	desiredSize := int32(params.DesiredSize)
	amountToAdd := int32(params.AmountToAdd)
	amountToSubtract := int32(params.AmountToSubtract)

	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster_id":       params.Cluster,
		"namespace":        params.Namespace,
		"nodePoolName":     params.NodePoolName,
		"desiredSize":      strconv.Itoa(int(desiredSize)),
		"amountToAdd":      strconv.Itoa(int(amountToAdd)),
		"amountToSubtract": strconv.Itoa(int(amountToSubtract)),
	})

	log.Debug("Scaling cluster node pool")

	_, provCluster, err := t.getProvisioningCluster(ctx, toolReq, log, params.Namespace, params.Cluster)
	if err != nil {
		log.Error("failed to get provisioning cluster", zap.Error(err))
		return nil, nil, err
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), toolReq.Extra.Header.Get(urlHeader), params.Namespace, LocalCluster, converter.K8sKindsToGVRs[converter.ProvisioningClusterResourceKind])
	if err != nil {
		return nil, nil, err
	}

	if provCluster.Spec.RKEConfig == nil {
		log.Error("RKEConfig is nil, cannot scale node pool")
		return nil, nil, fmt.Errorf("cluster %s has a nil RKEConfig, cannot scale node pool", params.Cluster)
	}

	if len(provCluster.Spec.RKEConfig.MachinePools) == 0 {
		log.Error("no machine pools found in RKEConfig, cannot scale node pool")
		return nil, nil, fmt.Errorf("cluster %s has no Node Pools, cannot scale", params.Cluster)
	}

	if desiredSize < 0 {
		log.Error("desired size must be greater than 0")
		return nil, nil, fmt.Errorf("desired size must be greater than or equal to 0")
	}

	if desiredSize == 0 && amountToAdd == 0 && amountToSubtract == 0 {
		log.Error("either desiredSize, amountToAdd, or amountToSubtract must be specified. A node pool cannot be scaled to 0 nodes")
		return nil, nil, fmt.Errorf("either desiredSize, amountToAdd, or amountToSubtract must be specified. A node pool cannot be scaled to 0 nodes")
	}

	if amountToAdd != 0 && amountToSubtract != 0 {
		log.Error("cannot specify both amountToAdd and amountToSubtract")
		return nil, nil, fmt.Errorf("cannot specify both amountToAdd and amountToSubtract")
	}

	found := false
	for i := range provCluster.Spec.RKEConfig.MachinePools {
		pool := &provCluster.Spec.RKEConfig.MachinePools[i]
		// accept either the concrete node pool name, or the node pool name prefixed with the cluster name (as seen in the Rancher UI)
		if params.NodePoolName == pool.Name || params.NodePoolName == provCluster.Name+"-"+pool.Name {
			log.Debug("node pool found in cluster RKEConfig, updating desired size", zap.Int32("current_size", *pool.Quantity))
			found = true

			if amountToAdd != 0 {
				desiredSize = *pool.Quantity + amountToAdd
			}

			if amountToSubtract != 0 {
				desiredSize = *pool.Quantity - amountToSubtract
			}

			if pool.EtcdRole && desiredSize < 3 {
				log.Error("will not scale etcd node pool below 3 nodes to prevent loss of quorum")
				return nil, nil, fmt.Errorf("refusing to scale etcd node pool below 3 nodes to prevent loss of quorum and potential data loss. instruct user must scale pool manually if absolutely required")
			}

			if desiredSize <= 0 {
				log.Error("A node pool cannot be scaled to 0 nodes or a negative number of nodes")
				return nil, nil, fmt.Errorf("A node pool cannot be scaled to 0 nodes or a negative number of nodes")
			}

			pool.Quantity = &desiredSize
			break
		}
	}

	if !found {
		err = fmt.Errorf("node pool %s not found in cluster %s", params.NodePoolName, params.Cluster)
		log.Error(err.Error())
		return nil, nil, err
	}

	objBytes, err := json.Marshal(provCluster)
	if err != nil {
		log.Error("failed to marshal resource", zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal resource: %w", err)
	}

	unstructuredObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(objBytes, unstructuredObj); err != nil {
		log.Error("failed to create unstructured resource", zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create unstructured object: %w", err)
	}

	log.Debug("Updating prov cluster with new node pool size")
	_, err = resourceInterface.Update(ctx, unstructuredObj, metav1.UpdateOptions{})
	if err != nil {
		log.Error("failed to update provisioning cluster with new node pool size", zap.Error(err))
		return nil, nil, err
	}
	log.Debug("Successfully updated provisioning cluster with new node pool size")

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{{
		Object: map[string]interface{}{
			"message": fmt.Sprintf("Successfully scaled node pool %s to desired size %d for cluster %s", params.NodePoolName, desiredSize, params.Cluster),
		},
	}}, params.Cluster)
	if err != nil {
		log.Error("failed to create mcp response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: mcpResponse,
		}},
	}, nil, nil
}
