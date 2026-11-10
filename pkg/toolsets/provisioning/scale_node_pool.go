package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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

func (params *scaleNodePoolParameters) validate(toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
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

	if err := validateClusterName(params.Cluster); err != nil {
		return nil, err
	}

	// The local cluster can never be scaled as it does not utilize
	// node pools, it's a special kind of imported cluster. A dedicated
	// error is returned so the agent knows that this isn't just a
	// generic problem with the tool.
	if strings.ToLower(params.Cluster) == "local" {
		log.Error("scaling is not supported for the local cluster")
		return nil, fmt.Errorf("scaling node pools in the local cluster is not supported")
	}

	for _, size := range []struct {
		name  string
		value int
	}{{"desiredSize", params.DesiredSize}, {"amountToAdd", params.AmountToAdd}, {"amountToSubtract", params.AmountToSubtract}} {
		if err := validateInt32(size.name, size.value); err != nil {
			return nil, err
		}
	}

	// A negative amount would scale the node pool in the opposite direction to the one requested.
	if params.AmountToAdd < 0 {
		return nil, fmt.Errorf("amountToAdd must not be negative, use amountToSubtract to remove nodes")
	}
	if params.AmountToSubtract < 0 {
		return nil, fmt.Errorf("amountToSubtract must not be negative, use amountToAdd to add nodes")
	}

	return log, nil
}

func (t *Tools) scaleClusterNodePool(ctx context.Context, toolReq *mcp.CallToolRequest, params scaleNodePoolParameters) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Scaling cluster node pool")

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), params.Namespace, LocalCluster, converter.K8sKindsToGVRs[converter.ProvisioningClusterResourceKind])
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
		return nil, nil, t.staleNodePoolError(ctx, log, params.Namespace, params.Cluster, params.NodePoolName, patchBytes, err)
	}

	log.Debug("Successfully patched provisioning cluster with new node pool size")

	removeMachinePoolSchemas(patchObj)
	// The provisioning cluster object is stored in the local cluster.
	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{patchObj}, LocalCluster)
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

	_, provCluster, err := t.getProvisioningCluster(ctx, log, params.Namespace, params.Cluster)
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

	poolIndex, pool := findMachinePool(provCluster, params.NodePoolName)
	if pool == nil {
		err = fmt.Errorf("node pool %s not found in cluster %s", params.NodePoolName, params.Cluster)
		log.Error(err.Error())
		return nil, err
	}

	// The cluster autoscaler adjusts the size of autoscaled pools, and would override a manual change.
	if isAutoscaled(*pool) {
		log.Error("Refusing to manually scale a node pool managed by the cluster autoscaler")
		return nil, fmt.Errorf("node pool %s is managed by the cluster autoscaler, which scales it between %d and %d nodes, so it cannot be scaled manually. Use %s to change its minimum and maximum size, or to disable autoscaling for the node pool", pool.Name, *pool.AutoscalingMinSize, *pool.AutoscalingMaxSize, relatedTool(toolReq, "updateNodePoolAutoscaling"))
	}

	oldQuantity := machinePoolQuantity(*pool)
	log.Debug("node pool found in cluster RKEConfig, updating desired size", zap.Int32("current_size", oldQuantity))

	if amountToAdd != 0 {
		desiredSize = oldQuantity + amountToAdd
	}

	if amountToSubtract != 0 {
		desiredSize = oldQuantity - amountToSubtract
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
		return nil, fmt.Errorf("a node pool cannot be scaled to 0 nodes or a negative number of nodes")
	}

	poolPath := fmt.Sprintf("/spec/rkeConfig/machinePools/%d", poolIndex)
	// "replace" requires the field to exist, so an unset quantity is added instead.
	quantityOp := "replace"
	if pool.Quantity == nil {
		quantityOp = "add"
	}
	patch := []map[string]any{
		// The pool is patched by index, so make sure the index still refers to the same pool when the patch is applied.
		testOp(poolPath+"/name", pool.Name),
		// The new size can be based on the current size, which may have changed after it was read.
		testOptionalOp(poolPath+"/quantity", pool.Quantity),
		// Make sure autoscaling wasn't enabled after the pool was read, as the cluster autoscaler would override the new size.
		testOptionalOp(poolPath+"/autoscalingMinSize", pool.AutoscalingMinSize),
		testOptionalOp(poolPath+"/autoscalingMaxSize", pool.AutoscalingMaxSize),
		{
			"op":    quantityOp,
			"path":  poolPath + "/quantity",
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
