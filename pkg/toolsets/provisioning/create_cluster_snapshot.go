package provisioning

import (
	"context"
	"encoding/json"
	"fmt"

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

const createSnapshotNote = "The etcd snapshot has been requested. Snapshots are taken asynchronously, use the listClusterSnapshots tool to check when it has completed."

type createClusterSnapshotParams struct {
	Cluster   string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace of the provisioning cluster. Defaults to fleet-default, or fleet-local for the local cluster"`
}

func (params *createClusterSnapshotParams) validate(toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
	params.Namespace = clusterResourcesNamespace(params.Cluster, params.Namespace)

	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":   params.Cluster,
		"namespace": params.Namespace,
	})

	if err := validateClusterName(params.Cluster); err != nil {
		return nil, err
	}

	return log, nil
}

// createClusterSnapshot triggers an etcd snapshot of an rke2 or k3s cluster provisioned by Rancher.
func (t *Tools) createClusterSnapshot(ctx context.Context, toolReq *mcp.CallToolRequest, params createClusterSnapshotParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Creating cluster etcd snapshot")

	patchBytes, err := t.createClusterSnapshotPatch(ctx, params, log)
	if err != nil {
		return nil, nil, err
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), params.Namespace, LocalCluster, converter.K8sKindsToGVRs[converter.ProvisioningClusterResourceKind])
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Patching prov cluster to request etcd snapshot", zap.String("patch", string(patchBytes)))

	patchObj, err := resourceInterface.Patch(ctx, params.Cluster, types.JSONPatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		log.Error("failed to patch provisioning cluster to request etcd snapshot", zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{patchObj}, LocalCluster, createSnapshotNote)
	if err != nil {
		log.Error("failed to create mcp response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// createClusterSnapshotPatch returns a JSON patch which bumps the etcd snapshot
// create generation of the provisioning cluster. Changing the generation is
// all that is required for Rancher to take a new snapshot.
func (t *Tools) createClusterSnapshotPatch(ctx context.Context, params createClusterSnapshotParams, log *zap.Logger) ([]byte, error) {
	_, provCluster, err := t.getProvisioningCluster(ctx, log, params.Namespace, params.Cluster)
	if err != nil {
		return nil, err
	}

	if provCluster.Spec.RKEConfig == nil {
		log.Error("RKEConfig is nil, cannot create etcd snapshot")
		return nil, fmt.Errorf("cluster %s is not an rke2 or k3s cluster provisioned by Rancher, etcd snapshots can only be taken for these clusters", params.Cluster)
	}

	generation := 1
	if provCluster.Spec.RKEConfig.ETCDSnapshotCreate != nil {
		generation = provCluster.Spec.RKEConfig.ETCDSnapshotCreate.Generation + 1
	}

	// "add" replaces the value if it already exists, so this works whether
	// or not a snapshot has been requested before.
	patch := []map[string]any{
		{
			"op":    "add",
			"path":  "/spec/rkeConfig/etcdSnapshotCreate",
			"value": map[string]any{"generation": generation},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		log.Error("failed to marshal patch", zap.Error(err))
		return nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	return patchBytes, nil
}

// clusterResourcesNamespace returns the namespace of the provisioning cluster,
// defaulting to fleet-local for the local cluster and fleet-default otherwise.
func clusterResourcesNamespace(cluster, namespace string) string {
	if namespace != "" && namespace != "default" {
		return namespace
	}
	if cluster == LocalCluster {
		return LocalClusterResourcesNamespace
	}
	return DefaultClusterResourcesNamespace
}
