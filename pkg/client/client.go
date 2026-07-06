package client

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var clusterIdsCache = sync.Map{}
var clustersDisplayNameToIDCache = sync.Map{}

// Client is a struct that provides methods for interacting with Kubernetes clusters.
type Client struct {
	insecure         bool
	rancherURL       string
	caBundle         []byte
	DynClientCreator func(*rest.Config) (dynamic.Interface, error)
	ClientSetCreator func(*rest.Config) (kubernetes.Interface, error)
}

// GetParams holds the parameters required to get a resource from k8s.
type GetParams struct {
	Cluster   string // The Cluster ID.
	Kind      string // The Kind of the Kubernetes resource (e.g., "pod", "deployment").
	Namespace string // The Namespace of the resource (optional).
	Name      string // The Name of the resource (optional).
	Token     string // The authentication Token for Steve.
}

// ListParams holds the parameters required to list resources from k8s.
type ListParams struct {
	Cluster       string // The Cluster ID.
	Kind          string // The Kind of the Kubernetes resource (e.g., "pod", "deployment").
	Namespace     string // The Namespace of the resource (optional).
	Name          string // The Name of the resource (optional).
	Token         string // The authentication Token for Steve.
	LabelSelector string // Optional LabelSelector string for the request.
	Limit         int64  // Optional maximum number of resources to return. 0 means no limit.
}

// NewClient creates and returns a new instance of the Client struct.
func NewClient(insecure bool, authzServerURL string) (*Client, error) {
	rancherURL, err := rancherURLFromAuthServerURL(authzServerURL)
	if err != nil {
		return nil, fmt.Errorf("parsing authz-server-url: %w", err)
	}
	if rancherURL == "" {
		if envURL := os.Getenv("RANCHER_URL"); envURL != "" {
			rancherURL = envURL
		} else {
			rancherURL, err = fetchRancherURL()
			if err != nil {
				return nil, fmt.Errorf("fetching internal-server-url from rancher: %w", err)
			}
		}
	}
	var caBundle []byte
	if !insecure {
		caBundle, err = fetchCABundle()
		if err != nil {
			return nil, fmt.Errorf("fetching internal-cacerts from rancher: %w", err)
		}
	}
	return &Client{
		caBundle:   caBundle,
		insecure:   insecure,
		rancherURL: rancherURL,
		DynClientCreator: func(cfg *rest.Config) (dynamic.Interface, error) {
			return dynamic.NewForConfig(cfg)
		},
		ClientSetCreator: func(cfg *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(cfg)
		},
	}, nil
}

func (c *Client) RancherURL() string {
	return c.rancherURL
}

// CreateClientSet creates a new Kubernetes clientset for the given Token and URL.
func (c *Client) CreateClientSet(ctx context.Context, token string, cluster string) (kubernetes.Interface, error) {
	clusterID, err := c.GetClusterID(ctx, token, cluster)
	if err != nil {
		return nil, err
	}
	restConfig, err := c.CreateRestConfig(token, clusterID)
	if err != nil {
		return nil, err
	}

	return c.ClientSetCreator(restConfig)
}

// GetResourceInterface returns a dynamic resource interface for the given Token, URL, Namespace, and GroupVersionResource.
func (c *Client) GetResourceInterface(ctx context.Context, token string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error) {
	clusterID, err := c.GetClusterID(ctx, token, cluster)
	if err != nil {
		return nil, err
	}
	restConfig, err := c.CreateRestConfig(token, clusterID)
	if err != nil {
		return nil, err
	}
	dynClient, err := c.DynClientCreator(restConfig)
	if err != nil {
		return nil, err
	}
	var resourceInterface dynamic.ResourceInterface = dynClient.Resource(gvr)
	if namespace != "" {
		resourceInterface = dynClient.Resource(gvr).Namespace(namespace)
	}

	return resourceInterface, nil
}

// GetResource retrieves a single Kubernetes resource by name.
// It returns the resource as an unstructured object or an error if the resource is not found.
func (c *Client) GetResource(ctx context.Context, params GetParams) (*unstructured.Unstructured, error) {
	resourceInterface, err := c.GetResourceInterface(ctx, params.Token, params.Namespace, params.Cluster, converter.K8sKindsToGVRs[strings.ToLower(params.Kind)])
	if err != nil {
		return nil, err
	}

	obj, err := resourceInterface.Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return obj, err
}

func (c *Client) GetResourceByGVR(ctx context.Context, params GetParams, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	resourceInterface, err := c.GetResourceInterface(ctx, params.Token, params.Namespace, params.Cluster, gvr)
	if err != nil {
		return nil, err
	}

	obj, err := resourceInterface.Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return obj, err
}

// GetResourceAtAnyAPIVersion queries the API server for all supported versions of the group and resource related to the passed kind. It then attempts to get the
// specified resource at each API version, stopping when one is found. This is needed when working with resources that may be periodically updated within
// Rancher, such as Cluster API resources.
func (c *Client) GetResourceAtAnyAPIVersion(ctx context.Context, params GetParams) (*unstructured.Unstructured, error) {
	currentGVK, ok := converter.K8sKindsToGVRs[strings.ToLower(params.Kind)]
	if !ok {
		return nil, fmt.Errorf("unknown kind: %s", params.Kind)
	}

	versions, err := c.getAPIVersionsForGR(ctx, params.Token, params.Cluster, schema.GroupResource{
		Group:    currentGVK.Group,
		Resource: currentGVK.Resource,
	})
	if err != nil {
		return nil, err
	}

	var item *unstructured.Unstructured
	for _, version := range versions {
		currentGVK.Version = version
		resourceInterface, err := c.GetResourceInterface(ctx, params.Token, params.Namespace, params.Cluster, currentGVK)
		if err != nil {
			return nil, err
		}

		item, err = resourceInterface.Get(ctx, params.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		break
	}

	if item == nil {
		return nil, errors.NewNotFound(schema.GroupResource{
			Group:    currentGVK.Group,
			Resource: currentGVK.Resource,
		}, params.Name)
	}

	return item, err
}

// GetResources lists Kubernetes resources matching the provided parameters.
// It supports optional label selectors for filtering and returns a slice of unstructured objects.
func (c *Client) GetResources(ctx context.Context, params ListParams) ([]*unstructured.Unstructured, error) {
	resourceInterface, err := c.GetResourceInterface(ctx, params.Token, params.Namespace, params.Cluster, converter.K8sKindsToGVRs[strings.ToLower(params.Kind)])
	if err != nil {
		return nil, err
	}

	opts := metav1.ListOptions{}
	if params.LabelSelector != "" {
		opts.LabelSelector = params.LabelSelector
	}
	if params.Limit > 0 {
		opts.Limit = params.Limit
	}
	list, err := resourceInterface.List(ctx, opts)
	if err != nil {
		return nil, err
	}

	objs := make([]*unstructured.Unstructured, len(list.Items))
	for i := range list.Items {
		objs[i] = &list.Items[i]
	}

	return objs, err
}

// GetResourcesAtAnyAPIVersion queries the API server for all supported versions of the group and resource related to the passed kind. It then attempts to get the
// specified resource at each API version, stopping when one is found. This is needed when working with resources that may be periodically updated within
// Rancher, such as Cluster API resources.
func (c *Client) GetResourcesAtAnyAPIVersion(ctx context.Context, params ListParams) ([]*unstructured.Unstructured, error) {
	currentGVK, ok := converter.K8sKindsToGVRs[strings.ToLower(params.Kind)]
	if !ok {
		return nil, fmt.Errorf("unknown kind: %s", params.Kind)
	}

	versions, err := c.getAPIVersionsForGR(ctx, params.Token, params.Cluster, schema.GroupResource{
		Group:    currentGVK.Group,
		Resource: currentGVK.Resource,
	})
	if err != nil {
		return nil, err
	}

	var list *unstructured.UnstructuredList
	for _, version := range versions {
		currentGVK.Version = version
		resourceInterface, err := c.GetResourceInterface(ctx, params.Token, params.Namespace, params.Cluster, currentGVK)
		if err != nil {
			return nil, err
		}
		opts := metav1.ListOptions{}
		if params.LabelSelector != "" {
			opts.LabelSelector = params.LabelSelector
		}
		if params.Limit > 0 {
			opts.Limit = params.Limit
		}
		list, err = resourceInterface.List(ctx, opts)
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		break
	}

	if list == nil || len(list.Items) == 0 {
		return nil, errors.NewNotFound(schema.GroupResource{
			Group:    currentGVK.Group,
			Resource: currentGVK.Resource,
		}, params.Name)
	}

	objs := make([]*unstructured.Unstructured, len(list.Items))
	for i := range list.Items {
		objs[i] = &list.Items[i]
	}

	return objs, err
}

// getClusterId returns the cluster's unique ID given either its cluster ID (metadata.name)
// or its display name (spec.displayName). It uses local caches to avoid redundant lookups.
//
// The lookup order is:
//  1. If the input is "local", return immediately.
//  2. Check in-memory caches for cluster ID or display name.
//  3. Query the cluster resource API by ID.
//  4. If not found, fall back to listing all clusters and matching by display name.
//
// both cluster ID and display name are cached for future lookups.
func (c *Client) GetClusterID(ctx context.Context, token string, clusterNameOrID string) (string, error) {
	// handle the special case for the local cluster, it always exists and is known by ID and displayName "local"
	if clusterNameOrID == "local" {
		return "local", nil
	}

	// check if the provided identifier is already known to be a cluster ID
	if _, ok := clusterIdsCache.Load(clusterNameOrID); ok {
		return clusterNameOrID, nil // it is a cluster ID
	}

	// check if the provided identifier matches a display name cached earlier
	if clusterID, exists := clustersDisplayNameToIDCache.Load(clusterNameOrID); exists {
		return clusterID.(string), nil
	}

	// try to fetch the cluster directly by its ID
	clusterInterface, err := c.GetResourceInterface(ctx, token, "", "local", converter.K8sKindsToGVRs["managementcluster"])
	if err != nil {
		return "", err
	}

	cluster, err := clusterInterface.Get(ctx, clusterNameOrID, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", err
		}

		// If not found by ID, try to locate it by display name.
		clusters, err := clusterInterface.List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", err
		}
		for _, cluster := range clusters.Items {
			clusterID := cluster.GetName()
			clusterIdsCache.Store(clusterID, struct{}{})

			displayName, found, err := unstructured.NestedString(
				cluster.Object,
				"spec",
				"displayName",
			)
			if err != nil {
				return "", err
			}

			if found {
				clustersDisplayNameToIDCache.Store(displayName, clusterID)

				// If the given identifier matches this display name, return its ID.
				if displayName == clusterNameOrID {
					return clusterID, nil
				}
			}
		}

		return "", fmt.Errorf("cluster '%s' not found", clusterNameOrID)
	}

	// clusterNameOrIDInput contains the cluster ID. Store it in the cache.
	clusterID := clusterNameOrID
	clusterIdsCache.Store(clusterID, struct{}{})

	displayName, found, err := unstructured.NestedString(
		cluster.Object,
		"spec",
		"displayName",
	)
	if err != nil {
		return "", err
	}
	if found {
		clustersDisplayNameToIDCache.Store(displayName, clusterID)
	}

	return clusterID, nil
}

// CreateRestConfig creates a new rest.Config for accessing a Kubernetes cluster through Rancher.
// It configures the cluster URL, authentication token, and TLS settings based on environment variables.
func (c *Client) CreateRestConfig(token string, clusterID string) (*rest.Config, error) {
	clusterURL := c.rancherURL + "/k8s/clusters/" + clusterID
	kubeconfig := clientcmdapi.NewConfig()
	cluster := &clientcmdapi.Cluster{
		Server: clusterURL,
	}
	if c.insecure {
		cluster.InsecureSkipTLSVerify = true
	} else if len(c.caBundle) > 0 {
		cluster.CertificateAuthorityData = c.caBundle
	}
	kubeconfig.Clusters["Cluster"] = cluster
	kubeconfig.AuthInfos["mcp"] = &clientcmdapi.AuthInfo{
		Token: token,
	}
	kubeconfig.Contexts["Cluster"] = &clientcmdapi.Context{
		Cluster:  "Cluster",
		AuthInfo: "mcp",
	}
	kubeconfig.CurrentContext = "Cluster"
	restConfig, err := clientcmd.NewNonInteractiveClientConfig(
		*kubeconfig,
		kubeconfig.CurrentContext,
		&clientcmd.ConfigOverrides{},
		nil,
	).ClientConfig()
	if err != nil {
		return nil, err
	}

	return restConfig, nil
}

// getAPIVersionsForGR queries the API server for all supported versions of the specified GroupResource.
// It returns a slice of version strings or an error if the query fails.
func (c *Client) getAPIVersionsForGR(ctx context.Context, token, cluster string, groupResource schema.GroupResource) ([]string, error) {
	clusterID, err := c.GetClusterID(ctx, token, cluster)
	if err != nil {
		return nil, err
	}
	restConfig, err := c.CreateRestConfig(token, clusterID)
	if err != nil {
		return nil, err
	}

	client, err := c.ClientSetCreator(restConfig)
	if err != nil {
		return nil, err
	}
	apiGroupList, err := client.Discovery().ServerGroups()
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, apiGroup := range apiGroupList.Groups {
		if apiGroup.Name == groupResource.Group {
			for _, version := range apiGroup.Versions {
				versions = append(versions, version.Version)
			}
		}
	}
	return versions, nil
}

// fetchRancherURL fetches the Rancher server URL from the internal-server-url Setting
// resource via the Kubernetes API using in-cluster config.
func fetchRancherURL() (string, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return "", fmt.Errorf("creating in-cluster config: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("creating dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "management.cattle.io",
		Version:  "v3",
		Resource: "settings",
	}

	obj, err := dynClient.Resource(gvr).Get(context.Background(), "internal-server-url", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting internal-server-url setting: %w", err)
	}

	value, _, _ := unstructured.NestedString(obj.Object, "value")
	return value, nil
}

// fetchCABundle fetches the CA certificate from the Rancher internal-cacerts Setting
// resource via the Kubernetes API using in-cluster config.
func fetchCABundle() ([]byte, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("creating in-cluster config: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "management.cattle.io",
		Version:  "v3",
		Resource: "settings",
	}

	obj, err := dynClient.Resource(gvr).Get(context.Background(), "internal-cacerts", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting internal-cacerts setting: %w", err)
	}

	value, _, _ := unstructured.NestedString(obj.Object, "value")
	if value == "" {
		return nil, nil
	}

	return []byte(value), nil
}

func rancherURLFromAuthServerURL(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	parsed, err := url.Parse(s)
	if err != nil {
		return "", err
	}

	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String(), nil
}
