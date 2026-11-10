package provisioning

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// These values are defined in github.com/rancher/rancher/pkg/capr and pkg/features,
// which can't be imported without pulling in the whole rancher module.
const (
	// clusterAutoscalerPausedAnnotation pauses the cluster-autoscaler of a cluster, scaling it down to zero replicas.
	clusterAutoscalerPausedAnnotation = "provisioning.cattle.io/cluster-autoscaler-paused"
	// clusterAutoscalerDeploymentReadyCondition reports the state of the cluster-autoscaler deployment in the downstream cluster.
	clusterAutoscalerDeploymentReadyCondition = "ClusterAutoscalerDeploymentReady"
	// clusterAutoscalingFeature is the Rancher feature flag that enables cluster-autoscaler support.
	clusterAutoscalingFeature = "cluster-autoscaling"
)

type clusterAutoscalingSummary struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	// Supported is false for clusters without machine pools, which can't be autoscaled by Rancher.
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// FeatureEnabled is omitted when the feature flag can't be read.
	FeatureEnabled *bool                        `json:"featureEnabled,omitempty"`
	Enabled        bool                         `json:"enabled"`
	Paused         bool                         `json:"paused"`
	Deployment     *autoscalerDeploymentSummary `json:"deployment,omitempty"`
	NodePools      []nodePoolAutoscalingSummary `json:"nodePools"`
}

type autoscalerDeploymentSummary struct {
	Ready   string `json:"ready"`
	Message string `json:"message,omitempty"`
}

type nodePoolAutoscalingSummary struct {
	Name       string   `json:"name"`
	Roles      []string `json:"roles"`
	Quantity   int32    `json:"quantity"`
	Autoscaled bool     `json:"autoscaled"`
	MinSize    *int32   `json:"minSize,omitempty"`
	MaxSize    *int32   `json:"maxSize,omitempty"`
	// Warning describes a problem with the autoscaling configuration of the node pool.
	Warning string `json:"warning,omitempty"`
}

// getClusterAutoscaling returns whether cluster autoscaling is supported, enabled and paused for a cluster,
// the state of the cluster-autoscaler deployment, and the autoscaling configuration of each node pool.
func (t *Tools) getClusterAutoscaling(ctx context.Context, toolReq *mcp.CallToolRequest, params inspectClusterParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Getting cluster autoscaling")

	_, provCluster, err := t.getProvisioningCluster(ctx, log, params.Namespace, params.Cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Warn("provisioning cluster not found, unsupported cluster type")
			return nil, nil, fmt.Errorf("provisioning cluster %s not found in namespace %s", params.Cluster, params.Namespace)
		}
		log.Error("failed to get provisioning cluster", zap.Error(err))
		return nil, nil, err
	}

	summary := clusterAutoscalingSummary{
		Cluster:        provCluster.Name,
		Namespace:      provCluster.Namespace,
		FeatureEnabled: t.clusterAutoscalingFeatureEnabled(ctx, log),
		// Rancher pauses the autoscaler whenever the annotation is set to a non-empty value.
		Paused:    provCluster.Annotations[clusterAutoscalerPausedAnnotation] != "",
		NodePools: []nodePoolAutoscalingSummary{},
	}

	if provCluster.Spec.RKEConfig == nil || len(provCluster.Spec.RKEConfig.MachinePools) == 0 {
		summary.Reason = "the cluster has no machine pools. Autoscaling is only supported for rke2 and k3s clusters provisioned by Rancher with machine pools"
	} else {
		summary.Supported = true
		for _, pool := range provCluster.Spec.RKEConfig.MachinePools {
			autoscaled := isAutoscaled(pool)
			if autoscaled {
				summary.Enabled = true
			}

			summary.NodePools = append(summary.NodePools, nodePoolAutoscalingSummary{
				Name:       pool.Name,
				Roles:      machinePoolRoles(pool),
				Quantity:   machinePoolQuantity(pool),
				Autoscaled: autoscaled,
				MinSize:    pool.AutoscalingMinSize,
				MaxSize:    pool.AutoscalingMaxSize,
				Warning:    autoscalingConfigWarning(pool),
			})
		}
	}

	for _, condition := range provCluster.Status.Conditions {
		if condition.Type == clusterAutoscalerDeploymentReadyCondition {
			summary.Deployment = &autoscalerDeploymentSummary{
				Ready:   string(condition.Status),
				Message: condition.Message,
			}
			break
		}
	}

	mcpResponse, err := response.CreateMcpResponseAny(summary, response.UIContext{
		Namespace: provCluster.Namespace,
		Kind:      "Cluster",
		Cluster:   LocalCluster,
		Name:      provCluster.Name,
		Type:      converter.K8sKindsToGVRs[converter.ProvisioningClusterResourceKind].Group + ".cluster",
	})
	if err != nil {
		log.Error("failed to create mcp response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// clusterAutoscalingFeatureEnabled returns whether the cluster-autoscaling feature flag is enabled in Rancher,
// or nil if it can't be determined, e.g. because the user is not allowed to read feature flags.
func (t *Tools) clusterAutoscalingFeatureEnabled(ctx context.Context, log *zap.Logger) *bool {
	feature, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: LocalCluster,
		Kind:    "feature",
		Name:    clusterAutoscalingFeature,
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		log.Warn("failed to get cluster-autoscaling feature flag", zap.Error(err))
		return nil
	}

	// spec.value overrides the default and is null when it isn't set.
	if value, found, err := unstructured.NestedBool(feature.Object, "spec", "value"); err == nil && found {
		return &value
	}
	if value, found, err := unstructured.NestedBool(feature.Object, "status", "default"); err == nil && found {
		return &value
	}

	return nil
}

// machinePoolRoles returns the roles of a machine pool, using the same names as the node-role.kubernetes.io labels.
func machinePoolRoles(pool provisioningV1.RKEMachinePool) []string {
	roles := []string{}
	if pool.ControlPlaneRole {
		roles = append(roles, "control-plane")
	}
	if pool.EtcdRole {
		roles = append(roles, "etcd")
	}
	if pool.WorkerRole {
		roles = append(roles, "worker")
	}
	return roles
}

// autoscalingConfigWarning returns a warning if only one of the minimum and maximum size of a machine pool is set,
// e.g. because the cluster was edited manually. The cluster autoscaler ignores node pools without both sizes.
func autoscalingConfigWarning(pool provisioningV1.RKEMachinePool) string {
	if (pool.AutoscalingMinSize == nil) == (pool.AutoscalingMaxSize == nil) {
		return ""
	}
	missing := "maxSize"
	if pool.AutoscalingMinSize == nil {
		missing = "minSize"
	}
	return fmt.Sprintf("autoscaling is not enabled because %s is not set, the cluster autoscaler requires both minSize and maxSize. Use updateNodePoolAutoscaling to set both, or to disable autoscaling and remove the partial configuration", missing)
}
