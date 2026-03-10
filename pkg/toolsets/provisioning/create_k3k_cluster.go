package provisioning

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ResourceLimits struct {
	CPU    string `json:"cpu,omitempty" jsonschema:"CPU limit, e.g., '1' or '500m'"`
	Memory string `json:"memory,omitempty" jsonschema:"Memory limit, e.g., '2Gi' or '512Mi'"`
}

type PersistenceConfig struct {
	Type             string `json:"type,omitempty" jsonschema:"Type of persistence, e.g., 'pvc' or 'ephemeral'"`
	StorageClassName string `json:"storageClassName,omitempty" jsonschema:"Storage class to use for PVC"`
	StorageRequest   string `json:"storageRequest,omitempty" jsonschema:"Size of the storage request, e.g., '5Gi'"`
}

type SyncConfig struct {
	PriorityClasses bool `json:"priorityClasses,omitempty" jsonschema:"sync priorityClasses"`
	Ingresses       bool `json:"ingresses,omitempty" jsonschema:"sync ingress resources"`
}

type createK3kClusterParams struct {
	Name          string             `json:"name" jsonschema:"the name of the K3k cluster"`
	Namespace     string             `json:"namespace" jsonschema:"the namespace where the K3k cluster will be created"`
	TargetCluster string             `json:"targetCluster" jsonschema:"the downstream cluster where the K3k resource will be applied"`
	Version       string             `json:"version,omitempty" jsonschema:"the k3s/k8s version for the cluster (e.g., v1.33.1-k3s1). Defaults to host cluster version"`
	Mode          string             `json:"mode,omitempty" jsonschema:"cluster mode (e.g., shared or virtual). Defaults to shared"`
	Servers       int32              `json:"servers,omitempty" jsonschema:"number of server (control plane) nodes. Defaults to 1"`
	Agents        int32              `json:"agents,omitempty" jsonschema:"number of agent (worker) nodes. Defaults to 0"`
	Sync          *SyncConfig        `json:"sync,omitempty" jsonschema:"resource synchronization options with boolean flags for priorityClasses and ingresses. Shared mode only"`
	ServerLimit   *ResourceLimits    `json:"serverLimit,omitempty" jsonschema:"resource constraints for server nodes (contains cpu and memory strings)"`
	WorkerLimit   *ResourceLimits    `json:"workerLimit,omitempty" jsonschema:"resource constraints for worker nodes (contains cpu and memory strings)"`
	Persistence   *PersistenceConfig `json:"persistence,omitempty" jsonschema:"storage settings for etcd data (contains type, storageClassName, storageRequest strings)"`
}

// createK3kClusterObj builds the unstructured K3k Cluster object from the given parameters.
func (t *Tools) createK3kClusterObj(params createK3kClusterParams) *unstructured.Unstructured {
	spec := map[string]interface{}{}

	if params.Version != "" {
		spec["version"] = params.Version
	}
	if params.Mode != "" {
		spec["mode"] = params.Mode
	}
	if params.Servers > 0 {
		spec["servers"] = int64(params.Servers)
	}
	if params.Agents > 0 {
		spec["agents"] = int64(params.Agents)
	}

	if params.Sync != nil {
		syncMap := map[string]interface{}{}
		if params.Sync.Ingresses {
			syncMap["ingresses"] = map[string]interface{}{
				"enabled": true,
			}
		}
		if params.Sync.PriorityClasses {
			syncMap["priorityClasses"] = map[string]interface{}{
				"enabled": true,
			}
		}
		if len(syncMap) > 0 {
			spec["sync"] = syncMap
		}
	}

	if params.ServerLimit != nil {
		lim := map[string]interface{}{}
		if params.ServerLimit.CPU != "" {
			lim["cpu"] = params.ServerLimit.CPU
		}
		if params.ServerLimit.Memory != "" {
			lim["memory"] = params.ServerLimit.Memory
		}
		if len(lim) > 0 {
			spec["serverLimit"] = lim
		}
	}

	if params.WorkerLimit != nil {
		lim := map[string]interface{}{}
		if params.WorkerLimit.CPU != "" {
			lim["cpu"] = params.WorkerLimit.CPU
		}
		if params.WorkerLimit.Memory != "" {
			lim["memory"] = params.WorkerLimit.Memory
		}
		if len(lim) > 0 {
			spec["workerLimit"] = lim
		}
	}

	if params.Persistence != nil {
		p := map[string]interface{}{}
		if params.Persistence.Type != "" {
			p["type"] = params.Persistence.Type
		}
		if params.Persistence.StorageClassName != "" {
			p["storageClassName"] = params.Persistence.StorageClassName
		}
		if params.Persistence.StorageRequest != "" {
			p["storageRequest"] = params.Persistence.StorageRequest
		}
		if len(p) > 0 {
			spec["persistence"] = p
		}
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k3k.io/v1beta1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"name":      params.Name,
				"namespace": params.Namespace,
			},
			"spec": spec,
		},
	}
}

// createK3kCluster creates a new K3k cluster using structured input parameters.
func (t *Tools) createK3kCluster(ctx context.Context, toolReq *mcp.CallToolRequest, params createK3kClusterParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createK3kCluster called")

	unstructuredObj := t.createK3kClusterObj(params)

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), toolReq.Extra.Header.Get(urlHeader), params.Namespace, params.TargetCluster, converter.K8sKindsToGVRs["k3kcluster"])
	if err != nil {
		zap.L().Error("failed to get resource interface", zap.String("tool", "createK3kCluster"), zap.Error(err))
		return nil, nil, err
	}

	obj, err := resourceInterface.Create(ctx, unstructuredObj, metav1.CreateOptions{})
	if err != nil {
		zap.L().Error("failed to create K3k cluster", zap.String("tool", "createK3kCluster"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create K3k cluster %s: %w", params.Name, err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, params.TargetCluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "createK3kCluster"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
