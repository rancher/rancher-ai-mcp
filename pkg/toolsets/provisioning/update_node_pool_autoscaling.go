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
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

type updateNodePoolAutoscalingParameters struct {
	Cluster      string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Namespace    string `json:"namespace" jsonschema:"the namespace where the resource is located. The default namespace will be used if not provided"`
	NodePoolName string `json:"nodePoolName" jsonschema:"the name of the node pool to update"`
	MinSize      int    `json:"minSize,omitempty" jsonschema:"the minimum number of nodes the cluster autoscaler can scale the node pool down to. Must be at least 1. Required unless disable is true"`
	MaxSize      int    `json:"maxSize,omitempty" jsonschema:"the maximum number of nodes the cluster autoscaler can scale the node pool up to. Must be greater than or equal to minSize. Required unless disable is true"`
	Disable      bool   `json:"disable,omitempty" jsonschema:"set to true to disable autoscaling for the node pool. The node pool keeps its current size. minSize and maxSize must not be provided when disabling"`
}

func (params *updateNodePoolAutoscalingParameters) validate(toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
	if params.Namespace == "" || params.Namespace == "default" {
		params.Namespace = DefaultClusterResourcesNamespace
	}

	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster_id":   params.Cluster,
		"namespace":    params.Namespace,
		"nodePoolName": params.NodePoolName,
		"minSize":      strconv.Itoa(params.MinSize),
		"maxSize":      strconv.Itoa(params.MaxSize),
		"disable":      strconv.FormatBool(params.Disable),
	})

	if err := validateClusterName(params.Cluster); err != nil {
		return nil, err
	}

	if strings.ToLower(params.Cluster) == LocalCluster {
		log.Error("autoscaling is not supported for the local cluster")
		return nil, fmt.Errorf("autoscaling node pools in the local cluster is not supported")
	}

	if params.Disable {
		if params.MinSize != 0 || params.MaxSize != 0 {
			return nil, fmt.Errorf("minSize and maxSize cannot be specified when disabling autoscaling")
		}
		return log, nil
	}

	for _, size := range []struct {
		name  string
		value int
	}{{"minSize", params.MinSize}, {"maxSize", params.MaxSize}} {
		if err := validateInt32(size.name, size.value); err != nil {
			return nil, err
		}
	}

	// The cluster autoscaler can't scale a Rancher node pool up from zero nodes,
	// as Rancher doesn't provide the node capacity information that requires.
	if params.MinSize < 1 {
		return nil, fmt.Errorf("minSize must be at least 1, the cluster autoscaler cannot scale a node pool up from zero nodes")
	}

	if params.MaxSize < params.MinSize {
		return nil, fmt.Errorf("maxSize (%d) must be greater than or equal to minSize (%d)", params.MaxSize, params.MinSize)
	}

	return log, nil
}

func (t *Tools) updateNodePoolAutoscaling(ctx context.Context, toolReq *mcp.CallToolRequest, params updateNodePoolAutoscalingParameters) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Updating node pool autoscaling")

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), params.Namespace, LocalCluster, converter.K8sKindsToGVRs[converter.ProvisioningClusterResourceKind])
	if err != nil {
		return nil, nil, err
	}

	patchBytes, notes, err := t.updateNodePoolAutoscalingPatch(ctx, toolReq, params, log)
	if err != nil {
		log.Error("failed to create patch for updating node pool autoscaling", zap.Error(err))
		return nil, nil, err
	}

	log.Debug("Patching prov cluster with new node pool autoscaling configuration", zap.String("patch", string(patchBytes)))

	patchObj, err := resourceInterface.Patch(ctx, params.Cluster, types.JSONPatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		log.Error("failed to patch provisioning cluster with new node pool autoscaling configuration", zap.Error(err))
		return nil, nil, t.staleNodePoolError(ctx, log, params.Namespace, params.Cluster, params.NodePoolName, patchBytes, err)
	}

	log.Debug("Successfully patched provisioning cluster with new node pool autoscaling configuration")

	removeMachinePoolSchemas(patchObj)
	// The provisioning cluster object is stored in the local cluster.
	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{patchObj}, LocalCluster, notes...)
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

// updateNodePoolAutoscalingPatch returns a JSON patch that updates the autoscaling configuration of a node pool,
// and notes describing any additional changes made to the node pool.
func (t *Tools) updateNodePoolAutoscalingPatch(ctx context.Context, toolReq *mcp.CallToolRequest, params updateNodePoolAutoscalingParameters, log *zap.Logger) ([]byte, []string, error) {
	_, provCluster, err := t.getProvisioningCluster(ctx, log, params.Namespace, params.Cluster)
	if err != nil {
		log.Error("failed to get provisioning cluster", zap.Error(err))
		return nil, nil, err
	}

	if provCluster.Spec.RKEConfig == nil || len(provCluster.Spec.RKEConfig.MachinePools) == 0 {
		log.Error("no machine pools found in RKEConfig, cannot update node pool autoscaling")
		return nil, nil, fmt.Errorf("cluster %s has no node pools. Autoscaling is only supported for rke2 and k3s clusters provisioned by Rancher with machine pools", params.Cluster)
	}

	poolIndex, pool := findMachinePool(provCluster, params.NodePoolName)
	if pool == nil {
		err := fmt.Errorf("node pool %s not found in cluster %s", params.NodePoolName, params.Cluster)
		log.Error(err.Error())
		return nil, nil, err
	}

	poolPath := fmt.Sprintf("/spec/rkeConfig/machinePools/%d", poolIndex)
	// The pool is patched by index, so make sure the index still refers to the same pool when the patch is applied.
	// Test operations are kept separate so they are all applied before the pool is modified.
	tests := []map[string]any{
		testOp(poolPath+"/name", pool.Name),
		// Don't overwrite or remove an autoscaling range that was added, changed or removed after it was read.
		testOptionalOp(poolPath+"/autoscalingMinSize", pool.AutoscalingMinSize),
		testOptionalOp(poolPath+"/autoscalingMaxSize", pool.AutoscalingMaxSize),
	}
	var ops []map[string]any
	var notes []string

	if params.Disable {
		if pool.AutoscalingMinSize == nil && pool.AutoscalingMaxSize == nil {
			return nil, nil, fmt.Errorf("autoscaling is not enabled for node pool %s", params.NodePoolName)
		}

		for _, field := range []struct {
			name  string
			value *int32
		}{
			{"autoscalingMinSize", pool.AutoscalingMinSize},
			{"autoscalingMaxSize", pool.AutoscalingMaxSize},
		} {
			if field.value != nil {
				ops = append(ops, map[string]any{"op": "remove", "path": poolPath + "/" + field.name})
			}
		}
	} else {
		// When the autoscaler adds or removes a single etcd node, it can leave the cluster with
		// an even number of etcd members, which risks losing quorum.
		if pool.EtcdRole {
			log.Error("Refusing to enable autoscaling for an etcd node pool")
			return nil, nil, fmt.Errorf("autoscaling is not supported for etcd node pools, as the cluster autoscaler can scale them to an even number of nodes, risking loss of etcd quorum. Use %s to change the size of etcd node pools", relatedTool(toolReq, "scaleClusterNodePool"))
		}

		minSize := int32(params.MinSize)
		maxSize := int32(params.MaxSize)
		ops = append(ops,
			map[string]any{"op": "add", "path": poolPath + "/autoscalingMinSize", "value": minSize},
			map[string]any{"op": "add", "path": poolPath + "/autoscalingMaxSize", "value": maxSize},
		)

		// The cluster autoscaler doesn't move a node pool into its range by itself,
		// so the pool is resized to the nearest bound when it is outside the new range.
		quantity := machinePoolQuantity(*pool)
		newQuantity := min(max(quantity, minSize), maxSize)
		if newQuantity != quantity {
			// The new size is based on the current size, which the cluster autoscaler may change at any time.
			tests = append(tests, testOptionalOp(poolPath+"/quantity", pool.Quantity))
			ops = append(ops, map[string]any{"op": "add", "path": poolPath + "/quantity", "value": newQuantity})
			notes = append(notes, fmt.Sprintf("node pool %s was resized from %d to %d nodes to fit within the new autoscaling range of %d to %d nodes", pool.Name, quantity, newQuantity, minSize, maxSize))
		}

		// The autoscaling range is saved either way, but the cluster autoscaler won't act on it in these cases.
		if enabled := t.clusterAutoscalingFeatureEnabled(ctx, log); enabled != nil && !*enabled {
			notes = append(notes, fmt.Sprintf("the %s feature is disabled in Rancher, so the cluster autoscaler won't scale node pool %s until a Rancher administrator enables the feature", clusterAutoscalingFeature, pool.Name))
		}
		if provCluster.Annotations[clusterAutoscalerPausedAnnotation] != "" {
			notes = append(notes, fmt.Sprintf("the cluster autoscaler is paused for cluster %s by the %s annotation, so it won't scale node pool %s until it is resumed", params.Cluster, clusterAutoscalerPausedAnnotation, pool.Name))
		}
	}

	patchBytes, err := json.Marshal(append(tests, ops...))
	if err != nil {
		log.Error("failed to marshal patch", zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	return patchBytes, notes, nil
}

// findMachinePool returns the index of and a pointer to the machine pool with the given name.
// It accepts either the concrete node pool name, or the node pool name prefixed with the cluster name (as seen in the Rancher UI).
// The returned pool is nil if no machine pool matches.
func findMachinePool(provCluster provisioningV1.Cluster, nodePoolName string) (int, *provisioningV1.RKEMachinePool) {
	for i := range provCluster.Spec.RKEConfig.MachinePools {
		pool := &provCluster.Spec.RKEConfig.MachinePools[i]
		if nodePoolName == pool.Name || nodePoolName == provCluster.Name+"-"+pool.Name {
			return i, pool
		}
	}
	return -1, nil
}
