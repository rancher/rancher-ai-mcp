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

func TestCreateK3kCluster(t *testing.T) {
	fakeToken := "fakeToken"
	scheme := runtime.NewScheme()

	tests := map[string]struct {
		params         createK3kClusterParams
		fakeDynClient  *dynamicfake.FakeDynamicClient
		expectedError  string
		expectedResult string
	}{
		"create cluster with minimum parameters (tests defaults)": {
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, k3kCustomListKinds(), newManagementCluster("downstream-1", true)),
			params: createK3kClusterParams{
				Name:          "min-cluster",
				Namespace:     "default",
				TargetCluster: "downstream-1",
			},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "k3k.io/v1beta1",
						"kind": "Cluster",
						"metadata": {
							"name": "min-cluster",
							"namespace": "default"
						},
						"spec": {}
					}
				],
				"uiContext": [
					{
						"cluster": "downstream-1",
						"kind": "Cluster",
						"name": "min-cluster",
						"namespace": "default",
						"type": "cluster"
					}
				]
			}`,
		},
		"create cluster with advanced optional parameters": {
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, k3kCustomListKinds(), newManagementCluster("downstream-2", true)),
			params: createK3kClusterParams{
				Name:          "adv-cluster",
				Namespace:     "default",
				TargetCluster: "downstream-2",
				Version:       "v1.30.0-k3s1",
				Mode:          "virtual",
				Servers:       3,
				Agents:        3,
				Sync: &SyncConfig{
					PriorityClasses: true,
					Ingresses:       true,
				},
				ServerLimit: &ResourceLimits{
					CPU:    "2",
					Memory: "4Gi",
				},
				Persistence: &PersistenceConfig{
					Type:             "dynamic",
					StorageClassName: "longhorn",
					StorageRequest:   "10Gi",
				},
			},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "k3k.io/v1beta1",
						"kind": "Cluster",
						"metadata": {
							"name": "adv-cluster",
							"namespace": "default"
						},
						"spec": {
							"agents": 3,
							"mode": "virtual",
							"persistence": {
								"storageClassName": "longhorn",
								"storageRequest": "10Gi",
								"type": "dynamic"
							},
							"serverLimit": {
								"cpu": "2",
								"memory": "4Gi"
							},
							"servers": 3,
							"sync": {
								"ingresses": {
									"enabled": true
								},
								"priorityClasses": {
									"enabled": true
								}
							},
							"version": "v1.30.0-k3s1"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "downstream-2",
						"kind": "Cluster",
						"name": "adv-cluster",
						"namespace": "default",
						"type": "cluster"
					}
				]
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

			result, _, err := tools.createK3kCluster(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				require.NoError(t, err)
				require.NotEmpty(t, result.Content)
				assert.JSONEq(t, test.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
