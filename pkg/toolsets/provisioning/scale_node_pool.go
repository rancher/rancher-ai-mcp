package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

type scaleNodePoolParameters struct {
	Cluster          string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Namespace        string `json:"namespace" jsonschema:"the namespace where the resource is located. The default namespace will be used if not provided"`
	NodePoolName     string `json:"nodePoolName" jsonschema:"the name of the node pool to scale"`
	DesiredSize      int    `json:"desiredSize,omitempty" jsonschema:"the desired size of the node pool. Overridden by amountToAdd and amountToSubtract if either are specified. If no specific size is provided, use zero"`
	AmountToAdd      int    `json:"amountToAdd,omitempty" jsonschema:"the amount of nodes to add to the node pool. If specified, desiredSize will be ignored. Cannot be used with amountToSubtract. If no specific amount is provided, use zero"`
	AmountToSubtract int    `json:"amountToSubtract,omitempty" jsonschema:"the amount of nodes to remove from the node pool. If specified, desiredSize will be ignored. Cannot be used with amountToAdd. If no specific amount is provided, use zero"`
}

func (t *Tools) scaleClusterNodePool(ctx context.Context, toolReq *mcp.CallToolRequest, params scaleNodePoolParameters) (*mcp.CallToolResult, any, error) {
	if params.Namespace == "" || params.Namespace == "default" {
		params.Namespace = DefaultClusterResourcesNamespace
	}

	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster_id":       params.Cluster,
		"namespace":        params.Namespace,
		"nodePoolName":     params.NodePoolName,
		"desiredSize":      strconv.Itoa(params.DesiredSize),
		"amountToAdd":      strconv.Itoa(params.AmountToAdd),
		"amountToSubtract": strconv.Itoa(params.AmountToSubtract),
	})

	log.Debug("Scaling cluster node pool")

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), t.rancherURL(toolReq), params.Namespace, LocalCluster, converter.K8sKindsToGVRs[converter.ProvisioningClusterResourceKind])
	if err != nil {
		return nil, nil, err
	}

	patchBytes, err := t.scaleClusterNodePoolPatch(ctx, toolReq, params, log)
	if err != nil {
		log.Error("failed to create patch for scaling node pool", zap.Error(err))
		return nil, nil, err
	}

	log.Debug("Patching prov cluster with new node pool size", zap.String("patch", string(patchBytes)))

	patchObj, err := resourceInterface.Patch(ctx, params.Cluster, types.JSONPatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		log.Error("failed to patch provisioning cluster with new node pool size", zap.Error(err))
		return nil, nil, err
	}

	log.Debug("Successfully patched provisioning cluster with new node pool size")

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{patchObj}, params.Cluster)
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

func (t *Tools) scaleClusterNodePoolPatch(ctx context.Context, toolReq *mcp.CallToolRequest, params scaleNodePoolParameters, log *zap.Logger) ([]byte, error) {
	desiredSize := int32(params.DesiredSize)
	amountToAdd := int32(params.AmountToAdd)
	amountToSubtract := int32(params.AmountToSubtract)

	_, provCluster, err := t.getProvisioningCluster(ctx, toolReq, log, params.Namespace, params.Cluster)
	if err != nil {
		log.Error("failed to get provisioning cluster", zap.Error(err))
		return nil, err
	}

	if provCluster.Spec.RKEConfig == nil {
		log.Error("RKEConfig is nil, cannot scale node pool")
		return nil, fmt.Errorf("cluster %s has a nil RKEConfig, cannot scale node pool", params.Cluster)
	}

	if len(provCluster.Spec.RKEConfig.MachinePools) == 0 {
		log.Error("no machine pools found in RKEConfig, cannot scale node pool")
		return nil, fmt.Errorf("cluster %s has no Node Pools, cannot scale", params.Cluster)
	}

	if desiredSize < 0 {
		log.Error("desired size must be greater than 0")
		return nil, fmt.Errorf("desired size must be greater than or equal to 0")
	}

	if desiredSize == 0 && amountToAdd == 0 && amountToSubtract == 0 {
		log.Error("either desiredSize, amountToAdd, or amountToSubtract must be specified. A node pool cannot be scaled to 0 nodes")
		return nil, fmt.Errorf("either desiredSize, amountToAdd, or amountToSubtract must be specified. A node pool cannot be scaled to 0 nodes")
	}

	if amountToAdd != 0 && amountToSubtract != 0 {
		log.Error("cannot specify both amountToAdd and amountToSubtract")
		return nil, fmt.Errorf("cannot specify both amountToAdd and amountToSubtract")
	}

	poolIndex := -1
	for i := range provCluster.Spec.RKEConfig.MachinePools {
		pool := &provCluster.Spec.RKEConfig.MachinePools[i]
		// accept either the concrete node pool name, or the node pool name prefixed with the cluster name (as seen in the Rancher UI)
		if params.NodePoolName != pool.Name && params.NodePoolName != provCluster.Name+"-"+pool.Name {
			continue
		}

		log.Debug("node pool found in cluster RKEConfig, updating desired size", zap.Int32("current_size", *pool.Quantity))
		poolIndex = i
		oldQuantity := *pool.Quantity

		if amountToAdd != 0 {
			desiredSize = *pool.Quantity + amountToAdd
		}

		if amountToSubtract != 0 {
			desiredSize = *pool.Quantity - amountToSubtract
		}

		if pool.EtcdRole {
			if desiredSize < 3 && desiredSize < oldQuantity {
				log.Error("Refusing to scale etcd node pool to less than 3 nodes to prevent loss of quorum")
				return nil, fmt.Errorf("scaling an etcd node pool to less than 3 nodes can result in a loss of quorum and potential data loss")
			}

			if desiredSize > 7 {
				log.Error("Refusing to scale etcd node pool to more than 7 nodes")
				return nil, fmt.Errorf("it is not recommended to have more than 7 etcd nodes in a cluster as it can lead to performance issues")
			}

			if desiredSize%2 == 0 {
				log.Error("Refusing to scale etcd node pool to an even number of nodes.")
				return nil, fmt.Errorf("etcd node pools should have an odd number of nodes to ensure fault tolerance and maintain quorum. Scaling to an even number of nodes can lead to split-brain scenarios and potential data loss")
			}
		}

		if desiredSize <= 0 {
			log.Error("Refusing to scale a node pool to 0 or a negative number of nodes")
			return nil, fmt.Errorf("A node pool cannot be scaled to 0 nodes or a negative number of nodes")
		}
		break
	}

	if poolIndex == -1 {
		err = fmt.Errorf("node pool %s not found in cluster %s", params.NodePoolName, params.Cluster)
		log.Error(err.Error())
		return nil, err
	}

	patch := []map[string]any{
		{
			"op":    "replace",
			"path":  fmt.Sprintf("/spec/rkeConfig/machinePools/%d/quantity", poolIndex),
			"value": desiredSize,
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		log.Error("failed to marshal patch", zap.Error(err))
		return nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	return patchBytes, nil
}
