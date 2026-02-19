package provisioning

import (
	"context"
	"testing"

	"mcp/pkg/client"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestScaleNodePool(t *testing.T) {
	tests := []struct {
		name           string
		fakeClientset  kubernetes.Interface
		fakeDynClient  *dynamicfake.FakeDynamicClient
		params         ScaleNodePoolParameters
		expectedError  string
		expectedResult string
	}{
		{
			name:          "scale node pool with desired size",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         true,
						ControlPlaneRole: true,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:      "test-cluster",
				Namespace:    "fleet-default",
				NodePoolName: "test-nodepool",
				DesiredSize:  3,
			},
			expectedError:  "",
			expectedResult: `{"llm":[{"message":"Successfully scaled node pool test-nodepool to desired size 3 for cluster test-cluster"}]}`,
		},
		{
			name:          "refuse to scale etcd node pool below 3 nodes",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         true,
						ControlPlaneRole: true,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
					{
						EtcdRole:         false,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:      "test-cluster",
				Namespace:    "fleet-default",
				NodePoolName: "test-nodepool",
				DesiredSize:  1,
			},
			expectedError:  "refusing to scale etcd node pool below 3 nodes to prevent loss of quorum and potential data loss. instruct user must scale pool manually if absolutely required",
			expectedResult: "",
		},
		{
			name:          "scale non-etcd pool to less than three nodes",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         true,
						ControlPlaneRole: true,
						WorkerRole:       true,
						Name:             "test-nodepool-etcd",
						Quantity:         toPtr[int32](int32(3)),
					},
					{
						EtcdRole:         false,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:      "test-cluster",
				Namespace:    "fleet-default",
				NodePoolName: "test-nodepool",
				DesiredSize:  3,
			},
			expectedError:  "",
			expectedResult: `{"llm":[{"message":"Successfully scaled node pool test-nodepool to desired size 3 for cluster test-cluster"}]}`,
		},
		{
			name:          "fail to provide desired size or amount to add/subtract",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         true,
						ControlPlaneRole: true,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 0,
				AmountToAdd:      0,
			},
			expectedError:  "either desiredSize, amountToAdd, or amountToSubtract must be specified. A node pool cannot be scaled to 0 nodes",
			expectedResult: "",
		},
		{
			name:          "try to scale to negative nodes",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         false,
						ControlPlaneRole: true,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      -10,
				AmountToSubtract: 0,
				AmountToAdd:      0,
			},
			expectedError:  "desired size must be greater than or equal to 0",
			expectedResult: "",
		},
		{
			name:          "add a single node",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         false,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 0,
				AmountToAdd:      1,
			},
			expectedError:  "",
			expectedResult: `{"llm":[{"message":"Successfully scaled node pool test-nodepool to desired size 2 for cluster test-cluster"}]}`,
		},
		{
			name:          "subtract a single node",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         false,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(2)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 1,
				AmountToAdd:      0,
			},
			expectedError:  "",
			expectedResult: `{"llm":[{"message":"Successfully scaled node pool test-nodepool to desired size 1 for cluster test-cluster"}]}`,
		},
		{
			name:          "refuse to subtract a node if it would scale pool to zero nodes",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         false,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 1,
				AmountToAdd:      0,
			},
			expectedError:  "A node pool cannot be scaled to 0 nodes or a negative number of nodes",
			expectedResult: "",
		},
		{
			name:          "refuse to subtract a node if it would scale pool to negative nodes",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         false,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(1)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 3,
				AmountToAdd:      0,
			},
			expectedError:  "A node pool cannot be scaled to 0 nodes or a negative number of nodes",
			expectedResult: "",
		},
		{
			name:          "refuse to subtract a node if it would result in etcd quorum loss",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         true,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(3)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 2,
				AmountToAdd:      0,
			},
			expectedError:  "refusing to scale etcd node pool below 3 nodes to prevent loss of quorum and potential data loss. instruct user must scale pool manually if absolutely required",
			expectedResult: "",
		},
		{
			name:          "scale a pool that doesn't exist",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         true,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(3)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "fake-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 2,
				AmountToAdd:      0,
			},
			expectedError:  "node pool fake-nodepool not found in cluster test-cluster",
			expectedResult: "",
		},
		{
			name:          "scale a cluster that doesn't have pools",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "fake-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 2,
				AmountToAdd:      0,
			},
			expectedError:  "cluster test-cluster has no Node Pools, cannot scale",
			expectedResult: "",
		},
		{
			name:          "attempt to both add and remove nodes",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
					{
						EtcdRole:         true,
						ControlPlaneRole: false,
						WorkerRole:       true,
						Name:             "test-nodepool",
						Quantity:         toPtr[int32](int32(3)),
					},
				})),
			params: ScaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "fake-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 2,
				AmountToAdd:      2,
			},
			expectedError:  "cannot specify both amountToAdd and amountToSubtract",
			expectedResult: "",
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

			result, _, err := tools.ScaleClusterNodePool(context.Background(), &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Name: "scaleClusterNodePool",
				},
				Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {testURL}, tokenHeader: {testToken}}},
			}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				assert.NoError(t, err)

				text, ok := result.Content[0].(*mcp.TextContent)
				assert.Truef(t, ok, "expected type *mcp.TextContent")

				assert.Truef(t, ok, "expected expectedResult to be a JSON string")
				assert.JSONEq(t, test.expectedResult, text.Text)
			}
		})
	}
}
