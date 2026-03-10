package core

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	toolsSet    = "rancher"
	toolsSetAnn = "toolset"
)

type toolsClient interface {
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
	GetResourceInterface(ctx context.Context, token string, url string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error)
	GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error)
	CreateClientSet(ctx context.Context, token string, url string, cluster string) (kubernetes.Interface, error)
	GetClusterID(ctx context.Context, token string, url string, clusterNameOrID string) (string, error)
}

// Tools contains all tools for the MCP server
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

// AddTools registers all Rancher Kubernetes tools with the provided MCP server.
// Each tool is configured with metadata identifying it as part of the rancher toolset.
func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getKubernetesResource",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Fetches a Kubernetes resource from the cluster. The namespace must be empty for all namespaces or cluster-wide resources.`},
		t.getResource,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "patchKubernetesResource",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Patches a Kubernetes resource using a JSON patch. Don't ask for confirmation. The namespace must be empty for cluster-wide resources. The content type used is application/json-patch+json. Returns the modified resource.

Example of the patch parameter:
[{"op": "replace", "path": "/spec/replicas", "value": 3}]`},
		t.updateKubernetesResource,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "patchKubernetesResourcePlan",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Plans to patch a Kubernetes resource using a JSON patch. It returns the JSON representation of the planned update without actually applying it in the cluster. Only used for displaying the patch when using human validation. The namespace must be empty for cluster-wide resources. The content type used is application/json-patch+json.

Example of the patch parameter:
[{"op": "replace", "path": "/spec/replicas", "value": 3}]`},
		t.updateKubernetesResourcePlan)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listKubernetesResources",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a list of Kubernetes resources. The namespace must be empty for all namespaces or cluster-wide resources.`},
		t.listKubernetesResources,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "inspectPod",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns all information related to a Pod. It includes its parent Deployment or StatefulSet, the CPU and memory consumption and the logs. It must be used for troubleshooting problems with pods.`},
		t.inspectPod,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getDeployment",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a Deployment and its Pods. It must be used for troubleshooting problems with deployments.`},
		t.getDeploymentDetails,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getNodeMetrics",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a list of all nodes in a specified Kubernetes cluster, including their current resource utilization metrics.`},
		t.getNodes,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "createKubernetesResource",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Creates a resource in a Kubernetes cluster. The namespace must be empty for cluster-wide resources.`},
		t.createKubernetesResource,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "createKubernetesResourcePlan",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Plans to create a resource in a Kubernetes cluster. It returns the JSON representation of the resource to be created without actually creating it in the cluster. Only used for displaying the resource when using human validation. The namespace must be empty for cluster-wide resources.`},
		t.createKubernetesResourcePlan)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getClusterImages",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a list of all container images for the specified clusters. If clusters is empty, returns images for all clusters.`},
		t.getClusterImages,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getProject",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a project resource and its associated namespaces.`},
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
		Name: "listClusters",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		// InputSchema explicitly includes "properties" to satisfy OpenAI's requirement
		// that object schemas must have a "properties" field, even when there are no parameters.
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Description: `Returns a list of all Rancher clusters, including local and downstream clusters.`},
		t.listClusters,
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
