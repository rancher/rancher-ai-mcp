package core

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

var fakeNode = &corev1.Node{
	ObjectMeta: metav1.ObjectMeta{
		Name: "node-1",
	},
	Status: corev1.NodeStatus{
		Capacity: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
		},
		Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
		},
	},
}

func nodeScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = metricsv1beta1.AddToScheme(scheme)
	return scheme
}

func TestGetNodes(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params        getNodesParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"get nodes": {
			params: getNodesParams{Cluster: "local"},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(nodeScheme(), map[schema.GroupVersionResource]string{
				{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}: "NodeMetricsList",
			}, fakeNode),
			requestURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"metadata": {"name": "node-1"},
						"spec": {},
						"status": {
							"allocatable": {"cpu": "4", "memory": "8Gi"},
							"capacity": {"cpu": "4", "memory": "8Gi"},
							"daemonEndpoints": {"kubeletEndpoint": {"Port": 0}},
							"nodeInfo": {
								"architecture": "",
								"bootID": "",
								"containerRuntimeVersion": "",
								"kernelVersion": "",
								"kubeProxyVersion": "",
								"kubeletVersion": "",
								"machineID": "",
								"operatingSystem": "",
								"osImage": "",
								"systemUUID": ""
							}
						}
					}
				]
			}`,
		},
		"get nodes when tool is configured with URL": {
			params: getNodesParams{Cluster: "local"},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(nodeScheme(), map[schema.GroupVersionResource]string{
				{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}: "NodeMetricsList",
			}, fakeNode),
			rancherURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"metadata": {"name": "node-1"},
						"spec": {},
						"status": {
							"allocatable": {"cpu": "4", "memory": "8Gi"},
							"capacity": {"cpu": "4", "memory": "8Gi"},
							"daemonEndpoints": {"kubeletEndpoint": {"Port": 0}},
							"nodeInfo": {
								"architecture": "",
								"bootID": "",
								"containerRuntimeVersion": "",
								"kernelVersion": "",
								"kubeProxyVersion": "",
								"kubeletVersion": "",
								"machineID": "",
								"operatingSystem": "",
								"osImage": "",
								"systemUUID": ""
							}
						}
					}
				]
			}`,
		},
		"get nodes - not found": {
			params:     getNodesParams{Cluster: "local"},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(nodeScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "nodes"}:                    "NodeList",
				{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}: "NodeMetricsList",
			}),
			expectedResult: `{"llm":"no resources found"}`,
		},
		"get nodes no rancherURL or request URL": {
			params: getNodesParams{Cluster: "local"},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(nodeScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "nodes"}:                    "NodeList",
				{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}: "NodeMetricsList",
			}),
			expectedError: "no URL for rancher request",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return tt.fakeDynClient, nil
				},
			}
			tools := NewTools(test.WrapClient(c, fakeToken, fakeUrl), tt.rancherURL)
			req := test.NewCallToolRequest(tt.requestURL)

			result, _, err := tools.getNodes(middleware.WithToken(t.Context(), fakeToken), req, tt.params)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
