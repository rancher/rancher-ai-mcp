package provisioning

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

func TestGetK3kClusters(t *testing.T) {
	fakeToken := "fakeToken"
	scheme := runtime.NewScheme()

	tests := map[string]struct {
		params         getK3kClustersParams
		fakeDynClient  *dynamicfake.FakeDynamicClient
		expectedResult string
		expectedError  string
	}{
		"get K3k clusters from single specified cluster": {
			params:        getK3kClustersParams{Clusters: []string{"local"}},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, k3kCustomListKinds(), newK3kCluster("test-k3k-cluster", "shared", "v1.33.1-k3s1", 3, 0)),
			expectedResult: `{
				"local": [
					{
						"name": "test-k3k-cluster",
						"spec": {
							"mode": "shared",
							"servers": 3,
							"agents": 0,
							"version": "v1.33.1-k3s1"
						},
						"status": {
							"phase": "Running",
							"ready": true
						}
					}
				]
			}`,
		},
		"get K3k clusters from empty cluster list (auto-discovery)": {
			params:        getK3kClustersParams{Clusters: []string{}},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, k3kCustomListKinds(), newManagementCluster("downstream-1", true), newK3kCluster("test-k3k-cluster", "shared", "v1.33.1-k3s1", 3, 0)),
			expectedResult: `{
				"downstream-1": [
					{
						"name": "test-k3k-cluster",
						"spec": {
							"mode": "shared",
							"servers": 3,
							"agents": 0,
							"version": "v1.33.1-k3s1"
						},
						"status": {
							"phase": "Running",
							"ready": true
						}
					}
				]
			}`,
		},
		"get K3k clusters from cluster with no K3k deployments": {
			params:        getK3kClustersParams{Clusters: []string{"local"}},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, k3kCustomListKinds()),
			expectedResult: `{
				"local": null
			}`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return test.fakeDynClient, nil
				},
			}
			tools := Tools{client: c}

			result, _, err := tools.getK3kClusters(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, test.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
