package provisioning

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	toolsSet    = "provisioning"
	toolsSetAnn = "toolset"
	urlHeader   = "R_url"
)

type toolsClient interface {
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResourceAtAnyAPIVersion(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResourcesAtAnyAPIVersion(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	GetResourceByGVR(ctx context.Context, params client.GetParams, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error)
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	GetResourceInterface(ctx context.Context, token string, url string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error)
}

// Tools contains tools for accessing provisioning information.
type Tools struct {
	client     toolsClient
	RancherURL string
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, rancherURL string) *Tools {
	return &Tools{
		client:     client,
		RancherURL: rancherURL,
	}
}

func (t *Tools) rancherURL(toolReq *mcp.CallToolRequest) string {
	if t.RancherURL == "" {
		return toolReq.Extra.Header.Get(urlHeader)
	}

	return t.RancherURL
}

func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "analyzeCluster",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Gets a cluster's complete configuration including provisioning and management clusters, the CAPI cluster, CAPI machines, and machine pool configs. 
					  This should be used when a complete overview of the clusters current state and its configuration is required.'

		Parameters:
		cluster (string): The name of the Kubernetes cluster
		namespace (string): The namespace where the resource is located. The default namespace will be used if not provided.
		`},
		t.AnalyzeCluster)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "analyzeClusterMachines",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Gets all Machine related resources for a cluster including Machines, MachineSets, and MachineDeployments.
					  This should be used when a summary or overview of just the existing machine resources is required.'

		Parameters:
		cluster (string): The name of the Kubernetes cluster
		namespace (string): The namespace where the resource is located. The default namespace will be used if not provided.
		`},
		t.AnalyzeClusterMachines)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getClusterMachine",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Gets a specific machine and its parent MachineSet and MachineDeployment.
   					  This should be used when detailed information about a specific machine is required.'

		Parameters:
		cluster (string): The name of the Kubernetes cluster
		machineName (string): The name of the machine to get
		`},
		t.GetClusterMachine)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "scaleClusterNodePool",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Changes the size of an existing node pool for an rke2 or k3s cluster.
   					  This should be used when the user wants to change the size of an existing node pool for an rke2 or k3s cluster.
                      Pools cannot be scaled to zero nodes, and etcd node pools cannot be scaled below 3 nodes to prevent loss of quorum.'

		Parameters:
		cluster (string): The name of the Kubernetes cluster.
	    namespace (string): The namespace where the resource is located. The default namespace will be used if not provided.
		nodePoolName (string): The name of the node pool to scale.
		desiredSize (int, optional): The desired size of the node pool. Overridden by amountToAdd and amountToSubtract if either are specified. If no specific size is provided, use zero.
		amountToAdd (int, optional): The amount of nodes to add to the node pool. If specified, desiredSize will be ignored. Cannot be used with amountToSubtract. If no specific amount is provided, use zero.
		amountToSubtract (int, optional): The amount of nodes to remove from the node pool. If specified, desiredSize will be ignored. Cannot be used with amountToAdd. If no specific amount is provided, use zero.
		`},
		t.ScaleClusterNodePool)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listK3kClusters",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List K3k virtual clusters deployed across downstream clusters.

		Parameters:
		clusters (array of strings): List of clusters to get virtual clusters from. Empty for return virtual clusters for all clusters.
		`},
		t.getK3kClusters)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "createK3kCluster",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Create a new K3k cluster in a specific downstream cluster.

		Parameters:
		name (string): The name of the K3k cluster.
		namespace (string): The namespace where the K3k cluster will be created.
		targetCluster (string): The downstream cluster where the K3k resource will be applied.
		version (string): Optional. The k3s/k8s version for the cluster (e.g., 'v1.33.1-k3s1'). Defaults to 'host cluster version'.
		mode (string): Optional. Cluster mode (e.g., 'shared' or 'virtual'). Defaults to 'shared'.
		servers (int): Optional. Number of server (control plane) nodes. Defaults to 1.
		agents (int): Optional. Number of agent (worker) nodes. Defaults to 0.
		sync (object): Optional. shared mode only. Resource synchronization options with boolean flags for 'priorityClasses' and 'ingresses'.
		serverLimit (object): Optional. Resource constraints for server nodes (contains 'cpu' and 'memory' strings).
		workerLimit (object): Optional. Resource constraints for worker nodes (contains 'cpu' and 'memory' strings).
		persistence (object): Optional. Storage settings for etcd data (contains 'type' ('dynamic' or 'ephemeral'), 'storageClassName', 'storageRequest' strings).
		`},
		t.createK3kCluster)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "createImportedCluster",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Creates an imported cluster within Rancher.
					  This should only be used when the user wants to create a new imported cluster. Do not use this tool when the user asks to create a new custom cluster.'
	
		Parameters:
		clusterName (string, required): The name of the cluster to be created.
	    description (string, optional): A short description added to the cluster.
		versionManagementSetting (string, optional): Specifies the version management setting for the cluster. Potential values are 'system-default', 'true', and 'false'. If not specified, the global version management setting will be used.
		`},
		t.CreateImportedCluster)
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listSupportedKubernetesVersions",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns the currently supported rke2 and k3s versions that can be provisioned.
   					  This should only be used when information about the supported rke2 and k3s is needed. This is often required to support provisioning custom and imported clusters.'

		Parameters:
		distribution (string, required): The distribution of the cluster, either "rke2" or "k3s".
		`},
		t.ListSupportedKubernetesVersions)
}
