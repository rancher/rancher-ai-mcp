package projects

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	toolsSet     = "rancher"
	toolsSetAnn  = "toolset"
	LocalCluster = "local"
)

type toolsClient interface {
	GetClusterID(ctx context.Context, token string, clusterNameOrID string) (string, error)
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	GetResourceInterface(ctx context.Context, token string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error)
}

// Tools contains tools for accessing project information.
type Tools struct {
	client   toolsClient
	ReadOnly bool
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient, readOnly bool) *Tools {
	return &Tools{
		client:   client,
		ReadOnly: readOnly,
	}
}

// AddTools registers all project tools with the provided MCP server.
func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getProject",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a project resource and its associated namespaces and members.`},
		t.getProject,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listProjects",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a list of project resources for a specified cluster.`},
		t.listProjects,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getResourceUsage",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns the resource usage for a namespace, project or all projects in a cluster.
Usage totals are provided for the entire project as well as broken down by namespace.
The resource usage includes CPU and memory requests, limits and actual usage, as well as the total number of pods.`},
		t.getResourceUsage,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getClusterResourceUsage",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns the overall resource usage of a cluster, as totals for the entire cluster as well as broken down by node.
For CPU, memory and pods it includes the capacity and allocatable resources of the nodes, the requests and limits of the pods scheduled on them, and the actual usage when the metrics server is available, with percentages relative to the allocatable resources.
If node metrics can't be retrieved, metricsUnavailableReason explains why, e.g. because the user isn't allowed to read metrics or the metrics server isn't installed.
Nodes without metrics, such as nodes that have just joined or are not ready, are listed in nodesWithoutMetrics, and cluster-wide actual usage is then omitted because it would be understated.
It also reports the number of ready nodes and the number of pending pods that could not be scheduled, which indicates that the cluster is running out of capacity.
Use this tool to check how much of a cluster's capacity is used, or whether a cluster needs to be scaled.`},
		t.getClusterResourceUsage,
	)

	if !t.ReadOnly {
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createProject",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Creates a project resource for a specified cluster with the given containerResourceQuota.`},
			t.createProject)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "createProjectPlan",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Plans to create a project resource for a specified cluster. It returns the JSON representation of the project to be created without actually creating it in the cluster. Only used for displaying the resource when using human validation.`},
			t.createProjectPlan)

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "moveNamespace",
			Meta: map[string]any{
				toolsSetAnn: toolsSet,
			},
			Description: `Moves a namespace to a project in the specified cluster.`},
			t.moveNamespace)
	}
}
