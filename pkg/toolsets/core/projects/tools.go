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
	}
}
