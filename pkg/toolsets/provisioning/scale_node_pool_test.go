package provisioning

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/stretchr/testify/assert"
)

func TestScaleNodePool(t *testing.T) {
	tests := []struct {
		name           string
		objects        []runtime.Object
		params         scaleNodePoolParameters
		expectedError  string
		expectedResult string
	}{
		{
			name: "scale node pool with desired size",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         true,
					ControlPlaneRole: true,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](1),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:      "test-cluster",
				Namespace:    "fleet-default",
				NodePoolName: "test-nodepool",
				DesiredSize:  3,
			},
			expectedError: "",
			expectedResult: `{
  "llm": [
    {
      "apiVersion": "provisioning.cattle.io/v1",
      "kind": "Cluster",
      "metadata": {
        "name": "test-cluster",
        "namespace": "fleet-default"
      },
      "spec": {
        "localClusterAuthEndpoint": {},
        "rkeConfig": {
          "chartValues": null,
          "dataDirectories": {},
          "machineGlobalConfig": null,
          "machinePoolDefaults": {},
          "machinePools": [
            {
              "controlPlaneRole": true,
              "etcdRole": true,
              "name": "test-nodepool",
              "quantity": 3,
              "workerRole": true
            }
          ],
          "upgradeStrategy": {
            "controlPlaneDrainOptions": {
              "deleteEmptyDirData": false,
              "disableEviction": false,
              "enabled": false,
              "force": false,
              "gracePeriod": 0,
              "skipWaitForDeleteTimeoutSeconds": 0,
              "timeout": 0
            },
            "workerDrainOptions": {
              "deleteEmptyDirData": false,
              "disableEviction": false,
              "enabled": false,
              "force": false,
              "gracePeriod": 0,
              "skipWaitForDeleteTimeoutSeconds": 0,
              "timeout": 0
            }
          }
        }
      },
      "status": {
        "clusterName": "c-m-abc123",
        "observedGeneration": 0,
        "ready": true
      }
    }
  ],
  "uiContext": [
    {
      "namespace": "fleet-default",
      "kind": "Cluster",
      "cluster": "local",
      "name": "test-cluster",
      "type": "provisioning.cattle.io.cluster"
    }
  ]
}`,
		},
		{
			name: "refuse to scale etcd node pool below 3 nodes",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         true,
					ControlPlaneRole: true,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](5),
				},
				{
					EtcdRole:         false,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](1),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:      "test-cluster",
				Namespace:    "fleet-default",
				NodePoolName: "test-nodepool",
				DesiredSize:  1,
			},
			expectedError:  "scaling an etcd node pool to less than 3 nodes can result in a loss of quorum and potential data loss",
			expectedResult: "",
		},
		{
			name: "refuse to manually scale an autoscaled node pool",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					WorkerRole:         true,
					Name:               "test-nodepool",
					Quantity:           ptr.To[int32](2),
					AutoscalingMinSize: ptr.To[int32](1),
					AutoscalingMaxSize: ptr.To[int32](5),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:      "test-cluster",
				Namespace:    "fleet-default",
				NodePoolName: "test-nodepool",
				DesiredSize:  3,
			},
			expectedError: "node pool test-nodepool is managed by the cluster autoscaler, which scales it between 1 and 5 nodes, so it cannot be scaled manually",
		},
		{
			name: "scale non-etcd pool to less than three nodes",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         true,
					ControlPlaneRole: true,
					WorkerRole:       true,
					Name:             "test-nodepool-etcd",
					Quantity:         ptr.To[int32](3),
				},
				{
					EtcdRole:         false,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](5),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:      "test-cluster",
				Namespace:    "fleet-default",
				NodePoolName: "test-nodepool",
				DesiredSize:  1,
			},
			expectedError: "",
			expectedResult: `{
  "llm": [
    {
      "apiVersion": "provisioning.cattle.io/v1",
      "kind": "Cluster",
      "metadata": {
        "name": "test-cluster",
        "namespace": "fleet-default"
      },
      "spec": {
        "localClusterAuthEndpoint": {},
        "rkeConfig": {
          "chartValues": null,
          "dataDirectories": {},
          "machineGlobalConfig": null,
          "machinePoolDefaults": {},
          "machinePools": [
            {
              "controlPlaneRole": true,
              "etcdRole": true,
              "name": "test-nodepool-etcd",
              "quantity": 3,
              "workerRole": true
            },
            {
              "name": "test-nodepool",
              "quantity": 1,
              "workerRole": true
            }
          ],
          "upgradeStrategy": {
            "controlPlaneDrainOptions": {
              "deleteEmptyDirData": false,
              "disableEviction": false,
              "enabled": false,
              "force": false,
              "gracePeriod": 0,
              "skipWaitForDeleteTimeoutSeconds": 0,
              "timeout": 0
            },
            "workerDrainOptions": {
              "deleteEmptyDirData": false,
              "disableEviction": false,
              "enabled": false,
              "force": false,
              "gracePeriod": 0,
              "skipWaitForDeleteTimeoutSeconds": 0,
              "timeout": 0
            }
          }
        }
      },
      "status": {
        "clusterName": "c-m-abc123",
        "observedGeneration": 0,
        "ready": true
      }
    }
  ],
  "uiContext": [
    {
      "namespace": "fleet-default",
      "kind": "Cluster",
      "cluster": "local",
      "name": "test-cluster",
      "type": "provisioning.cattle.io.cluster"
    }
  ]
}`,
		},
		{
			name: "fail to provide desired size or amount to add/subtract",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         true,
					ControlPlaneRole: true,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](1),
				},
			})},
			params: scaleNodePoolParameters{
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
			name: "try to scale to negative nodes",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         false,
					ControlPlaneRole: true,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](1),
				},
			})},
			params: scaleNodePoolParameters{
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
			name: "add a single node",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         false,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](1),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 0,
				AmountToAdd:      1,
			},
			expectedError: "",
			expectedResult: `{
  "llm": [
    {
      "apiVersion": "provisioning.cattle.io/v1",
      "kind": "Cluster",
      "metadata": {
        "name": "test-cluster",
        "namespace": "fleet-default"
      },
      "spec": {
        "localClusterAuthEndpoint": {},
        "rkeConfig": {
          "chartValues": null,
          "dataDirectories": {},
          "machineGlobalConfig": null,
          "machinePoolDefaults": {},
          "machinePools": [
            {
              "name": "test-nodepool",
              "quantity": 2,
              "workerRole": true
            }
          ],
          "upgradeStrategy": {
            "controlPlaneDrainOptions": {
              "deleteEmptyDirData": false,
              "disableEviction": false,
              "enabled": false,
              "force": false,
              "gracePeriod": 0,
              "skipWaitForDeleteTimeoutSeconds": 0,
              "timeout": 0
            },
            "workerDrainOptions": {
              "deleteEmptyDirData": false,
              "disableEviction": false,
              "enabled": false,
              "force": false,
              "gracePeriod": 0,
              "skipWaitForDeleteTimeoutSeconds": 0,
              "timeout": 0
            }
          }
        }
      },
      "status": {
        "clusterName": "c-m-abc123",
        "observedGeneration": 0,
        "ready": true
      }
    }
  ],
  "uiContext": [
    {
      "namespace": "fleet-default",
      "kind": "Cluster",
      "cluster": "local",
      "name": "test-cluster",
      "type": "provisioning.cattle.io.cluster"
    }
  ]
}`,
		},
		{
			name: "subtract a single node",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         false,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](2),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 1,
				AmountToAdd:      0,
			},
			expectedError: "",
			expectedResult: `{
  "llm": [
    {
      "apiVersion": "provisioning.cattle.io/v1",
      "kind": "Cluster",
      "metadata": {
        "name": "test-cluster",
        "namespace": "fleet-default"
      },
      "spec": {
        "localClusterAuthEndpoint": {},
        "rkeConfig": {
          "chartValues": null,
          "dataDirectories": {},
          "machineGlobalConfig": null,
          "machinePoolDefaults": {},
          "machinePools": [
            {
              "name": "test-nodepool",
              "quantity": 1,
              "workerRole": true
            }
          ],
          "upgradeStrategy": {
            "controlPlaneDrainOptions": {
              "deleteEmptyDirData": false,
              "disableEviction": false,
              "enabled": false,
              "force": false,
              "gracePeriod": 0,
              "skipWaitForDeleteTimeoutSeconds": 0,
              "timeout": 0
            },
            "workerDrainOptions": {
              "deleteEmptyDirData": false,
              "disableEviction": false,
              "enabled": false,
              "force": false,
              "gracePeriod": 0,
              "skipWaitForDeleteTimeoutSeconds": 0,
              "timeout": 0
            }
          }
        }
      },
      "status": {
        "clusterName": "c-m-abc123",
        "observedGeneration": 0,
        "ready": true
      }
    }
  ],
  "uiContext": [
    {
      "namespace": "fleet-default",
      "kind": "Cluster",
      "cluster": "local",
      "name": "test-cluster",
      "type": "provisioning.cattle.io.cluster"
    }
  ]
}`,
		},
		{
			name: "refuse to subtract a node if it would scale pool to zero nodes",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         false,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](1),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 1,
				AmountToAdd:      0,
			},
			expectedError:  "a node pool cannot be scaled to 0 nodes or a negative number of nodes",
			expectedResult: "",
		},
		{
			name: "refuse to subtract a node if it would scale pool to negative nodes",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         false,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](1),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 3,
				AmountToAdd:      0,
			},
			expectedError:  "a node pool cannot be scaled to 0 nodes or a negative number of nodes",
			expectedResult: "",
		},
		{
			name: "refuse to subtract a node if it would result in etcd quorum loss",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         true,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](3),
				},
			})},
			params: scaleNodePoolParameters{
				Cluster:          "test-cluster",
				Namespace:        "fleet-default",
				NodePoolName:     "test-nodepool",
				DesiredSize:      0,
				AmountToSubtract: 2,
				AmountToAdd:      0,
			},
			expectedError:  "scaling an etcd node pool to less than 3 nodes can result in a loss of quorum and potential data loss",
			expectedResult: "",
		},
		{
			name: "scale a pool that doesn't exist",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         true,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](3),
				},
			})},
			params: scaleNodePoolParameters{
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
			name:    "scale a cluster that doesn't have pools",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{})},
			params: scaleNodePoolParameters{
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
			name: "attempt to both add and remove nodes",
			objects: []runtime.Object{newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{
				{
					EtcdRole:         true,
					ControlPlaneRole: false,
					WorkerRole:       true,
					Name:             "test-nodepool",
					Quantity:         ptr.To[int32](3),
				},
			})},
			params: scaleNodePoolParameters{
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
			tools := newTestProvisioningTools(test.objects...)

			result, _, err := tools.scaleClusterNodePool(middleware.WithToken(t.Context(), testToken), newToolRequest("scaleClusterNodePool"), test.params)

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

func TestScaleNodePoolParametersValidate(t *testing.T) {
	tests := map[string]struct {
		params        scaleNodePoolParameters
		expectedError string
	}{
		"valid desired size": {
			params: scaleNodePoolParameters{Cluster: "test-cluster", NodePoolName: "worker", DesiredSize: 3},
		},
		"negative amount to add": {
			params:        scaleNodePoolParameters{Cluster: "test-cluster", NodePoolName: "worker", AmountToAdd: -1},
			expectedError: "amountToAdd must not be negative, use amountToSubtract to remove nodes",
		},
		"negative amount to subtract": {
			params:        scaleNodePoolParameters{Cluster: "test-cluster", NodePoolName: "worker", AmountToSubtract: -1},
			expectedError: "amountToSubtract must not be negative, use amountToAdd to add nodes",
		},
		"desired size that would wrap around to a valid size": {
			params:        scaleNodePoolParameters{Cluster: "test-cluster", NodePoolName: "worker", DesiredSize: 1<<32 + 3},
			expectedError: "desiredSize (4294967299) is out of range",
		},
		"amount to add that would wrap around to a valid amount": {
			params:        scaleNodePoolParameters{Cluster: "test-cluster", NodePoolName: "worker", AmountToAdd: 1<<32 + 1},
			expectedError: "amountToAdd (4294967297) is out of range",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := tt.params.validate(newToolRequest("scaleClusterNodePool"))
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			assert.NoError(t, err)
		})
	}
}
