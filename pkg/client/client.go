// Package client talks to Rancher and the clusters it manages, through Rancher's Kubernetes API proxy and its own APIs.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/rancher-ai-mcp/pkg/converter"
)

var (
	clusterIDsCache              = sync.Map{}
	clustersDisplayNameToIDCache = sync.Map{}
)

// Client is a struct that provides methods for interacting with Kubernetes clusters.
type Client struct {
	insecure         bool
	rancherURL       string
	caBundle         []byte
	DynClientCreator func(*rest.Config) (dynamic.Interface, error)
	ClientSetCreator func(*rest.Config) (kubernetes.Interface, error)

	// httpClient is shared by requests to Rancher's own APIs so their connections are reused.
	httpClientOnce sync.Once
	httpClient     *http.Client
}

// rancherRequestTimeout bounds requests to Rancher's own APIs, which have no other deadline unless the
// caller's context has one.
const rancherRequestTimeout = 30 * time.Second

// rancherHTTPClient returns the HTTP client for requests to Rancher's own APIs, creating it on first use.
func (c *Client) rancherHTTPClient() *http.Client {
	c.httpClientOnce.Do(func() {
		c.httpClient = &http.Client{
			Timeout: rancherRequestTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: c.insecure, //nolint:gosec
					RootCAs:            rootCAsFromBundle(c.caBundle),
				},
			},
		}
	})

	return c.httpClient
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

	return newClient(insecure, rancherURL, caBundle), nil
}

// NewClientWithURL creates a Client that talks to the given Rancher URL.
//
// Unlike NewClient, it does not use the in-cluster config to discover the
// Rancher URL or CA bundle, so it can be used outside of a cluster. When
// insecure is false the system trust store is used to verify Rancher's certificate.
func NewClientWithURL(insecure bool, rancherURL string) (*Client, error) {
	rancherURL = strings.TrimSuffix(rancherURL, "/")
	if rancherURL == "" {
		return nil, errors.New("rancher URL is required")
	}

	return newClient(insecure, rancherURL, nil), nil
}

func newClient(insecure bool, rancherURL string, caBundle []byte) *Client {
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
	}
}

// RancherURL returns the URL of the Rancher server the client talks to.
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
		return nil, fmt.Errorf("get cluster ID error: %w", err)
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
		return nil, fmt.Errorf("get resource interface error: %w", err)
	}

	obj, err := resourceInterface.Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return obj, err
}

// GetResourceByGVR gets a single resource by its group, version and resource, for kinds the converter does not know.
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
			if apierrors.IsNotFound(err) {
				continue
			}

			return nil, err
		}

		break
	}

	if item == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{
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
			if apierrors.IsNotFound(err) {
				continue
			}

			return nil, err
		}

		break
	}

	if list == nil || len(list.Items) == 0 {
		return nil, apierrors.NewNotFound(schema.GroupResource{
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

// GetClusterID returns the cluster's unique ID given either its cluster ID (metadata.name)
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
	if _, ok := clusterIDsCache.Load(clusterNameOrID); ok {
		return clusterNameOrID, nil // it is a cluster ID
	}

	// check if the provided identifier matches a display name cached earlier
	if cached, exists := clustersDisplayNameToIDCache.Load(clusterNameOrID); exists {
		if clusterID, ok := cached.(string); ok {
			return clusterID, nil
		}
	}

	// try to fetch the cluster directly by its ID
	clusterInterface, err := c.GetResourceInterface(ctx, token, "", "local", converter.K8sKindsToGVRs["managementcluster"])
	if err != nil {
		return "", err
	}

	cluster, err := clusterInterface.Get(ctx, clusterNameOrID, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return "", err
		}

		// If not found by ID, try to locate it by display name.
		clusters, err := clusterInterface.List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", err
		}

		for _, cluster := range clusters.Items {
			clusterID := cluster.GetName()
			clusterIDsCache.Store(clusterID, struct{}{})

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
	clusterIDsCache.Store(clusterID, struct{}{})

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

// GetRancherURL performs an authenticated HTTP request against rancherURL+path.
//
// The request is authenticated using the provided token as Bearer token in the Authorization header,
// unless token is empty, as for Rancher's public endpoints.
// If body is non-nil it is JSON-encoded and sent as the request body.
// The request is sent using the specified HTTP method (e.g., GET, POST).
//
// The response is expected to have a 2xx status code.
//
// Any other status code is returned as a *apierrors.StatusError (from
// k8s.io/apimachinery/pkg/api/errors), wrapped with the request URL, so callers
// can use the same helpers as for Kubernetes API errors, e.g.
// apierrors.IsUnauthorized for an invalid or expired token (401) and
// apierrors.IsForbidden when the token is valid but lacks permission (403).
//
// If v is non-nil the response body is assumed to be JSON and unmarshalled into
// it. A 204 No Content response is not unmarshalled.
func (c *Client) GetRancherURL(ctx context.Context, token string, method string, rancherPath string, body any, v any) error {
	requestPath := c.rancherURL + rancherPath

	var reqBody io.Reader

	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshalling request body for %s: %w", requestPath, err)
		}

		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestPath, reqBody)
	if err != nil {
		return fmt.Errorf("creating HTTP request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Public endpoints are called without a token, and an empty bearer token is not a valid header.
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.rancherHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("performing HTTP request to %s: %w", requestPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		statusErr := apierrors.NewGenericServerResponse(resp.StatusCode, method, schema.GroupResource{}, "", serverMessage(resp.Body), 0, true)
		return fmt.Errorf("request to %s failed: %w", requestPath, statusErr)
	}

	if v != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return fmt.Errorf("decoding response body from %s: %w", requestPath, err)
		}
	}

	return nil
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

	value, _, err := unstructured.NestedString(obj.Object, "value")

	return value, err
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

	value, found, err := unstructured.NestedString(obj.Object, "value")
	if err != nil {
		return nil, fmt.Errorf("reading internal-cacerts value: %w", err)
	}

	if !found || strings.TrimSpace(value) == "" {
		return nil, nil
	}

	return []byte(value), nil
}

// maxErrorBodyBytes limits how much of an error response body is read.
const maxErrorBodyBytes = 4096

// serverMessage extracts a human-readable message from an error response body.
//
// Both Kubernetes Status objects and Rancher (Norman) API errors carry a
// "message" field, which is preferred. Otherwise the trimmed body is returned.
func serverMessage(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	if err != nil {
		return ""
	}

	var withMessage struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(b, &withMessage); err == nil && withMessage.Message != "" {
		return withMessage.Message
	}

	return strings.TrimSpace(string(b))
}

func rootCAsFromBundle(bundle []byte) *x509.CertPool {
	if len(bundle) == 0 {
		return nil
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(bundle)

	return pool
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
