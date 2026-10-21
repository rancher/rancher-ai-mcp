package provisioning

import (
	"context"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	ToolsSet    = "provisioning"
	toolsSetAnn = "toolset"
)

type toolsClient interface {
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResourceAtAnyAPIVersion(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResourcesAtAnyAPIVersion(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	GetResourceByGVR(ctx context.Context, params client.GetParams, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error)
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	GetResourceInterface(ctx context.Context, token string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error)
	CreateClientSet(ctx context.Context, token string, cluster string) (kubernetes.Interface, error)
	RancherURL() string
}

// Tools contains tools for accessing provisioning information.
type Tools struct {
	client       toolsClient
	toolboxImage string
	ReadOnly     bool
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, readOnly bool) *Tools {
	return &Tools{
		client:       client,
		ReadOnly:     readOnly,
		toolboxImage: os.Getenv("TOOLBOX_IMAGE"),
	}
}

func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "analyzeCluster",
		Meta: map[string]any{
			toolsSetAnn: ToolsSet,
		},
		Description: `Returns cluster configuration and status, including provisioning and management clusters, the CAPI cluster, and machine-pool configs. Optionally include CAPI MachineDeployments, MachineSets, and/or Machines with the corresponding include flags. Use for a cluster-wide overview. Use analyzeClusterMachines or getClusterMachine for focused machine inspection.`},
		t.analyzeCluster)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "analyzeClusterMachines",
		Meta: map[string]any{
			toolsSetAnn: ToolsSet,
		},
		Description: `Lists selected CAPI MachineDeployments, MachineSets, and/or Machines for a cluster. Set at least one include flag. Use when the machine name is unknown or an issue spans multiple machines; use getClusterMachine for a known machine.`},
		t.analyzeClusterMachines)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getClusterMachine",
		Meta: map[string]any{
			toolsSetAnn: ToolsSet,
		},
		Description: `Gets a specific CAPI machine and optionally its owning MachineSet and MachineDeployment. Set at least one include flag. Use when the machine name is known. Use analyzeClusterMachines for issues spanning multiple machines.`},
		t.getClusterMachine)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listK3kClusters",
		Meta: map[string]any{
			toolsSetAnn: ToolsSet,
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"clusters": map[string]any{
					"type":        "array",
					"description": "list of clusters to get virtual clusters from. Empty to return virtual clusters for all clusters",
					"items": map[string]any{
						"type": "string",
					},
				},
			},
		},
		Description: `List K3k virtual clusters deployed across downstream clusters.`},
		t.getK3kClusters)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listSupportedKubernetesVersions",
		Meta: map[string]any{
			toolsSetAnn: ToolsSet,
		},
		Description: `Returns the currently supported rke2 and k3s versions that can be provisioned.
This should only be used when information about the supported rke2 and k3s is needed. This is often required to support provisioning custom and imported clusters.`},
		t.listSupportedKubernetesVersions)

	if !t.ReadOnly {
		if t.toolboxImage != "" {
			// while investigateFailedPlanApplication doesn't create resources
			// permanently, it needs to create a short-lived job to scrape
			// downstream logs.
			mcp.AddTool(mcpServer, &mcp.Tool{
				Name: "investigateFailedProvisioningPlanApplication",
				Meta: map[string]any{
					toolsSetAnn: ToolsSet,
				},
				Description: `Diagnoses a failing machine plan on a single node. Deploys a short-lived privileged job that reads the node's rancher-system-agent journald logs.
Only call this after a plan failure has been observed on that node.
Returns log excerpts and plan fields (checksum, failure-count, last-apply-time, max-failures, plan-revision, plan-state, probe-statuses), not the full plan.
Not available for the local cluster or non-CAPI clusters (aks, eks, gke, k3k, etc.). Do not show the logs unless explicitly asked.`},
				t.investigateFailedPlanApplication)

			mcp.AddTool(mcpServer, &mcp.Tool{
				Name: "investigateFailedProvisioningPlanApplicationPlan",
				Meta: map[string]any{
					toolsSetAnn: ToolsSet,
				},
				Description: `Dry-run of investigateFailedProvisioningPlanApplication. Returns the JSON of the privileged log-gathering job that would be deployed, without deploying or running it.`},
				t.investigateFailedPlanApplicationPlan)
		}

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "scaleClusterNodePool",
			Meta: map[string]any{
				toolsSetAnn: ToolsSet,
			},
			Description: `Changes the size of an existing node pool for an rke2 or k3s cluster.
This should be used when the user wants to change the size of an existing node pool for an rke2 or k3s cluster.
Pools cannot be scaled to zero nodes, and etcd node pools cannot be scaled below 3 nodes to prevent loss of quorum.
The local cluster does not support node pool scaling.`},
			t.scaleClusterNodePool)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "scaleClusterNodePoolPlan",
			Meta: map[string]any{
				toolsSetAnn: ToolsSet,
			},
			Description: `Plans to change the size of an existing node pool for an rke2 or k3s cluster. It returns the JSON representation of the updated node pool resource without actually applying the change in the cluster.
Only used for displaying the resource when using human validation. This should be used when the user wants to change the size of an existing node pool for an rke2 or k3s cluster.
Pools cannot be scaled to zero nodes, and etcd node pools cannot be scaled below 3 nodes to prevent loss of quorum.
The local cluster does not support node pool scaling.`},
			t.scaleClusterNodePoolPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createK3kCluster",
			Meta: map[string]any{
				toolsSetAnn: ToolsSet,
			},
			Description: `Create a new K3k cluster in a specific downstream cluster.`},
			t.createK3kCluster)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createK3kClusterPlan",
			Meta: map[string]any{
				toolsSetAnn: ToolsSet,
			},
			Description: `Plans to create a new K3k cluster in a specific downstream cluster. It returns the JSON representation of the resource to be created without actually creating it. Only used for displaying the resource when using human validation.`},
			t.createK3kClusterPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createImportedCluster",
			Meta: map[string]any{
				toolsSetAnn: ToolsSet,
			},
			Description: `Creates an imported cluster within Rancher.
This should only be used when the user wants to create a new imported cluster. Do not use this tool when the user asks to create a new custom cluster.`},
			t.createImportedCluster)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createImportedClusterPlan",
			Meta: map[string]any{
				toolsSetAnn: ToolsSet,
			},
			Description: `Plans to create an imported cluster within Rancher. It returns the JSON representation of the resource to be created without actually creating it in the cluster. Only used for displaying the resource when using human validation.
This should only be used when the user wants to create a new imported cluster. Do not use this tool when the user asks to create a new custom cluster.`},
			t.createImportedClusterPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createCustomCluster",
			Meta: map[string]any{
				toolsSetAnn: ToolsSet,
			},
			Description: `Creates a custom cluster within Rancher.
This should only be used when the user wants to create a new custom cluster. Do not use this tool if a user asks to create an imported cluster.`},
			t.createCustomCluster)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createCustomClusterPlan",
			Meta: map[string]any{
				toolsSetAnn: ToolsSet,
			},
			Description: `Plans to create a custom cluster within Rancher. It returns the JSON representation of the resource to be created without actually creating it in the cluster. Only used for displaying the resource when using human validation.
This should only be used when the user wants to create a new custom cluster. Do not use this tool if a user asks to create an imported cluster.`},
			t.createCustomClusterPlan)
	}

}
