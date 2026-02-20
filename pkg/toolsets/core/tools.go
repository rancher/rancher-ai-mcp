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
		Description: `Fetches a Kubernetes resource from the cluster.
		Parameters:
		name (string, required): The name of the Kubernetes resource.
		kind (string, required): The kind of the Kubernetes resource (e.g. 'Deployment', 'Service').
		cluster (string): The name of the Kubernetes cluster managed by Rancher.
		namespace (string, optional): The namespace of the resource. It must be empty for all namespaces or cluster-wide resources.
		
		Returns:
		The JSON representation of the requested Kubernetes resource.`},
		t.getResource,
	)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "patchKubernetesResource",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Patches a Kubernetes resource using a JSON patch. Don't ask for confirmation.'
		Parameters:
		kind (string): The type of Kubernetes resource to patch (e.g., Pod, Deployment, Service).
		namespace (string): The namespace where the resource is located. It must be empty for cluster-wide resources.
		name (string): The name of the specific resource to patch.
		cluster (string): The name of the Kubernetes cluster.
		patch (json): Patch to apply. This must be a JSON object. The content type used is application/json-patch+json.
		Returns the modified resource.
		
		Example of the patch parameter:
		[{"op": "replace", "path": "/spec/replicas", "value": 3}]`},
		t.updateKubernetesResource)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "listKubernetesResources",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a list of kubernetes resources.'
		Parameters:
		kind (string): The type of Kubernetes resource to patch (e.g., Pod, Deployment, Service).
		namespace (string): The namespace where the resource are located. It must be empty for all namespaces or cluster-wide resources.
		cluster (string): The name of the Kubernetes cluster.`},
		t.listKubernetesResources)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "inspectPod",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns all information related to a Pod. It includes its parent Deployment or StatefulSet, the CPU and memory consumption and the logs. It must be used for troubleshooting problems with pods.'
		Parameters:
		namespace (string): The namespace where the resource are located.
		cluster (string): The name of the Kubernetes cluster.
		name (string): The name of the Pod.`},
		t.inspectPod)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getDeployment",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a Deployment and its Pods. It must be used for troubleshooting problems with deployments.'
		Parameters:
		namespace (string): The namespace where the resource are located.
		cluster (string): The name of the Kubernetes cluster.
		name (string): The name of the Deployment.`},
		t.getDeploymentDetails)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getNodeMetrics",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a list of all nodes in a specified Kubernetes cluster, including their current resource utilization metrics.'
		Parameters:
		cluster (string): The name of the Kubernetes cluster.`},
		t.getNodes)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "createKubernetesResource",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Creates a resource in a kubernetes cluster.'
		Parameters:
		kind (string): The type of Kubernetes resource to patch (e.g., Pod, Deployment, Service).
		namespace (string): The namespace where the resource is located. It must be empty for cluster-wide resources.
		name (string): The name of the specific resource to patch.
		cluster (string): The name of the Kubernetes cluster. Empty for single container pods.
		resource (json): Resource to be created. This must be a JSON object.`},
		t.createKubernetesResource)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "getClusterImages",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Returns a list of all container images for the specified clusters.'
		Parameters:
		clusters (array of strings): List of clusters to get images from. Empty for return images for all clusters.`},
		t.getClusterImages)
}
