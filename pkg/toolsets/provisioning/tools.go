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
	ReadOnly   bool
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, rancherURL string, readOnly bool) *Tools {
	return &Tools{
		client:     client,
		RancherURL: rancherURL,
		ReadOnly:   readOnly,
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
This should be used when a complete overview of the clusters current state and its configuration is required.`},
		t.analyzeCluster)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "analyzeClusterMachines",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Gets all Machine related resources for a cluster including Machines, MachineSets, and MachineDeployments.
This should be used when a summary or overview of just the existing machine resources is required.`},
		t.analyzeClusterMachines)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getClusterMachine",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Gets a specific machine and its parent MachineSet and MachineDeployment.
This should be used when detailed information about a specific machine is required.`},
		t.getClusterMachine)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listK3kClusters",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `List K3k virtual clusters deployed across downstream clusters.`},
		t.getK3kClusters)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listSupportedKubernetesVersions",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns the currently supported rke2 and k3s versions that can be provisioned.
This should only be used when information about the supported rke2 and k3s is needed. This is often required to support provisioning custom and imported clusters.`},
		t.listSupportedKubernetesVersions)

	if !t.ReadOnly {
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "scaleClusterNodePool",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Changes the size of an existing node pool for an rke2 or k3s cluster.
This should be used when the user wants to change the size of an existing node pool for an rke2 or k3s cluster.
Pools cannot be scaled to zero nodes, and etcd node pools cannot be scaled below 3 nodes to prevent loss of quorum.`},
			t.scaleClusterNodePool)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "scaleClusterNodePoolPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to change the size of an existing node pool for an rke2 or k3s cluster. It returns the JSON representation of the updated node pool resource without actually applying the change in the cluster.
Only used for displaying the resource when using human validation. This should be used when the user wants to change the size of an existing node pool for an rke2 or k3s cluster.
Pools cannot be scaled to zero nodes, and etcd node pools cannot be scaled below 3 nodes to prevent loss of quorum.`},
			t.scaleClusterNodePoolPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createK3kCluster",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Create a new K3k cluster in a specific downstream cluster.`},
			t.createK3kCluster)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createK3kClusterPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to create a new K3k cluster in a specific downstream cluster. It returns the JSON representation of the resource to be created without actually creating it. Only used for displaying the resource when using human validation.`},
			t.createK3kClusterPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createImportedCluster",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Creates an imported cluster within Rancher.
This should only be used when the user wants to create a new imported cluster. Do not use this tool when the user asks to create a new custom cluster.`},
			t.createImportedCluster)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createImportedClusterPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to create an imported cluster within Rancher. It returns the JSON representation of the resource to be created without actually creating it in the cluster. Only used for displaying the resource when using human validation.
This should only be used when the user wants to create a new imported cluster. Do not use this tool when the user asks to create a new custom cluster.`},
			t.createImportedClusterPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createCustomCluster",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Creates a custom cluster within Rancher.
This should only be used when the user wants to create a new custom cluster. Do not use this tool if a user asks to create an imported cluster.`},
			t.createCustomCluster)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createCustomClusterPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to create a custom cluster within Rancher. It returns the JSON representation of the resource to be created without actually creating it in the cluster. Only used for displaying the resource when using human validation.
This should only be used when the user wants to create a new custom cluster. Do not use this tool if a user asks to create an imported cluster.`},
			t.createCustomClusterPlan)
	}

}
