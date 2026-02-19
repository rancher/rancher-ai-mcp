package provisioning

import (
	"context"
	"mcp/pkg/client"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestCreateCustomCluster(t *testing.T) {
	tests := []struct {
		name            string
		fakeClientset   kubernetes.Interface
		fakeDynClient   *dynamicfake.FakeDynamicClient
		params          CreateCustomClusterParams
		finalK8sVersion string
		expectedError   string
		rke2KdmOutput   string
		k3sKdmOutput    string
	}{
		{
			name:          "Empty cluster name",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:  "",
				Distribution: "rke2",
			},
			expectedError: "ClusterName is required",
		},
		{
			name:          "invalid distribution",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:  "test",
				Distribution: "gke",
			},
			expectedError: "invalid value for Distribution: gke. Valid values are 'rke2' and 'k3s'",
		},
		{
			name:          "invalid CNI",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:  "test",
				Distribution: "rke2",
				CNI:          "faker",
			},
			expectedError: "unsupported CNI \"faker\". Valid values are \"calico\", \"canal\", \"cilium\", \"flannel\", \"multus,canal\", \"multus,cilium\", \"multus,calico\", \"none\"",
		},
		{
			name:          "invalid rke2 version",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:       "test",
				Distribution:      "rke2",
				CNI:               "calico",
				KubernetesVersion: "v2.28.0",
			},
			rke2KdmOutput: createDummyKDMData("v1.32.4+rke2r1", "v1.32.3+rke2r1"),
			expectedError: "unsupported Kubernetes version: v2.28.0 for distribution: rke2. Only support versions [v1.32.4+rke2r1 v1.32.3+rke2r1]",
		},
		{
			name:          "invalid k3s version",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:       "test",
				Distribution:      "k3s",
				CNI:               "calico",
				KubernetesVersion: "v2.28.0",
			},
			k3sKdmOutput:  createDummyKDMData("v1.32.4+k3s1", "v1.32.3+k3s1"),
			expectedError: "unsupported Kubernetes version: v2.28.0 for distribution: k3s. Only support versions [v1.32.4+k3s1 v1.32.3+k3s1]",
		},
		{
			name:          "valid rke2 cluster",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:       "test",
				Distribution:      "rke2",
				CNI:               "calico",
				KubernetesVersion: "v1.32.4+rke2r1",
			},
			finalK8sVersion: "v1.32.4+rke2r1",
			rke2KdmOutput:   createDummyKDMData("v1.32.4+rke2r1", "v1.32.3+rke2r1"),
		},
		{
			name:          "valid k3s cluster",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:       "test",
				Distribution:      "k3s",
				CNI:               "calico",
				KubernetesVersion: "v1.32.4+k3s1",
			},
			finalK8sVersion: "v1.32.4+k3s1",
			k3sKdmOutput:    createDummyKDMData("v1.32.4+k3s1", "v1.32.3+k3s1"),
		},
		{
			name:          "valid k3s cluster using latest release",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:       "test",
				Distribution:      "k3s",
				CNI:               "calico",
				KubernetesVersion: "v1.32.4",
			},
			finalK8sVersion: "v1.32.4+k3s3",
			k3sKdmOutput:    createDummyKDMData("v1.32.4+k3s1", "v1.32.4+k3s2", "v1.32.4+k3s3"),
		},
		{
			name:          "valid rke2 cluster using latest release",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:       "test",
				Distribution:      "rke2",
				CNI:               "calico",
				KubernetesVersion: "v1.32.4",
			},
			finalK8sVersion: "v1.32.4+rke2r3",
			rke2KdmOutput:   createDummyKDMData("v1.32.4+rke2r1", "v1.32.4+rke2r2", "v1.32.4+rke2r3"),
		},
		{
			name:          "valid k3s with no KDM data",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateCustomClusterParams{
				ClusterName:       "test",
				Distribution:      "k3s",
				CNI:               "calico",
				KubernetesVersion: "v1.32.4",
			},
			finalK8sVersion: "v1.32.4+k3s3",
			k3sKdmOutput:    createDummyKDMData(""),
			rke2KdmOutput:   createDummyKDMData("v1.32.4+rke2r1", "v1.32.4+rke2r2", "v1.32.4+rke2r3"),
			expectedError:   "unsupported Kubernetes version: v1.32.4 for distribution: k3s. Only support versions []",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := &client.Client{
				ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
					return test.fakeClientset, nil
				},
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return test.fakeDynClient, nil
				},
			}
			tools := Tools{client: c}

			// setup dummy KDM endpoint
			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1-k3s-release/releases":
					w.Write([]byte(test.k3sKdmOutput))
				case "/v1-rke2-release/releases":
					w.Write([]byte(test.rke2KdmOutput))
				}
			}))

			result, _, err := tools.CreateCustomCluster(context.Background(), &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Name: "createCustomCluster",
				},
				Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {svr.URL}, tokenHeader: {testToken}}},
			}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				assert.NoError(t, err)

				text, ok := result.Content[0].(*mcp.TextContent)
				assert.Truef(t, ok, "expected type *mcp.TextContent")

				assert.Truef(t, ok, "expected expectedResult to be a JSON string")
				assert.JSONEq(t, createCustomClusterOutput(test.params, test.finalK8sVersion), text.Text)
			}

			svr.Close()
		})
	}
}
