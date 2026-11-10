package provisioning

import (
	"fmt"
	"testing"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScaleNodePoolPlan(t *testing.T) {
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
			expectedResult: `[
  {
    "type": "update",
    "payload": [
` + expectedScaleTestOps(0, "test-nodepool", 1) + `
      {
        "op": "replace",
        "path": "/spec/rkeConfig/machinePools/0/quantity",
        "value": 3
      }
    ],
    "resource": {
      "name": "test-cluster",
      "kind": "provisioningcluster",
      "cluster": "local",
      "namespace": "fleet-default"
    }
  }
]`,
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
			expectedResult: `[
  {
    "type": "update",
    "payload": [
` + expectedScaleTestOps(1, "test-nodepool", 5) + `
      {
        "op": "replace",
        "path": "/spec/rkeConfig/machinePools/1/quantity",
        "value": 1
      }
    ],
    "resource": {
      "name": "test-cluster",
      "kind": "provisioningcluster",
      "cluster": "local",
      "namespace": "fleet-default"
    }
  }
]`,
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
			expectedResult: `[
  {
    "type": "update",
    "payload": [
` + expectedScaleTestOps(0, "test-nodepool", 1) + `
      {
        "op": "replace",
        "path": "/spec/rkeConfig/machinePools/0/quantity",
        "value": 2
      }
    ],
    "resource": {
      "name": "test-cluster",
      "kind": "provisioningcluster",
      "cluster": "local",
      "namespace": "fleet-default"
    }
  }
]`,
		},
		{
			name: "try to scale an etcd pool to an even number of nodes",
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
				AmountToAdd:      1,
			},
			expectedError: "etcd node pools should have an odd number of nodes to ensure fault tolerance and maintain quorum. Scaling to an even number of nodes can lead to split-brain scenarios and potential data loss",
		},
		{
			name: "try to scale an etcd pool to more than 7 nodes",
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
				DesiredSize:      8,
				AmountToSubtract: 0,
				AmountToAdd:      0,
			},
			expectedError: "it is not recommended to have more than 7 etcd nodes in a cluster as it can lead to performance issues",
		},
		{
			name: "add two node to an etcd pool with less than three initial nodes",
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
				AmountToAdd:      2,
			},
			expectedError: "",
			expectedResult: `[
  {
    "type": "update",
    "payload": [
` + expectedScaleTestOps(0, "test-nodepool", 1) + `
      {
        "op": "replace",
        "path": "/spec/rkeConfig/machinePools/0/quantity",
        "value": 3
      }
    ],
    "resource": {
      "name": "test-cluster",
      "kind": "provisioningcluster",
      "cluster": "local",
      "namespace": "fleet-default"
    }
  }
]`,
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
			expectedResult: `[
  {
    "type": "update",
    "payload": [
` + expectedScaleTestOps(0, "test-nodepool", 2) + `
      {
        "op": "replace",
        "path": "/spec/rkeConfig/machinePools/0/quantity",
        "value": 1
      }
    ],
    "resource": {
      "name": "test-cluster",
      "kind": "provisioningcluster",
      "cluster": "local",
      "namespace": "fleet-default"
    }
  }
]`,
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

			result, _, err := tools.scaleClusterNodePoolPlan(middleware.WithToken(t.Context(), testToken), newToolRequest("scaleClusterNodePoolPlan"), test.params)

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

// expectedScaleTestOps returns the JSON patch test operations that a scale patch starts with, followed by a comma,
// for a pool at index with the given name and quantity that isn't autoscaled.
func expectedScaleTestOps(index int, name string, quantity int) string {
	path := fmt.Sprintf("/spec/rkeConfig/machinePools/%d", index)
	return fmt.Sprintf(`{"op": "test", "path": "%[1]s/name", "value": %[2]q},
      {"op": "test", "path": "%[1]s/quantity", "value": %[3]d},
      {"op": "test", "path": "%[1]s/autoscalingMinSize", "value": null},
      {"op": "test", "path": "%[1]s/autoscalingMaxSize", "value": null},`, path, name, quantity)
}

func TestScaleNodePoolPlanWithoutQuantity(t *testing.T) {
	// Rancher creates a single node when the quantity isn't set.
	tools := newTestProvisioningTools(newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123",
		[]provisioningV1.RKEMachinePool{{Name: "worker", WorkerRole: true}}))

	result, _, err := tools.scaleClusterNodePoolPlan(middleware.WithToken(t.Context(), testToken), newToolRequest("scaleClusterNodePoolPlan"),
		scaleNodePoolParameters{Cluster: "test-cluster", Namespace: "fleet-default", NodePoolName: "worker", AmountToAdd: 1})
	require.NoError(t, err)

	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected type *mcp.TextContent")
	assert.JSONEq(t, `[{
		"type": "update",
		"payload": [
			{"op": "test", "path": "/spec/rkeConfig/machinePools/0/name", "value": "worker"},
			{"op": "test", "path": "/spec/rkeConfig/machinePools/0/quantity", "value": null},
			{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize", "value": null},
			{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize", "value": null},
			{"op": "add", "path": "/spec/rkeConfig/machinePools/0/quantity", "value": 2}
		],
		"resource": {"name": "test-cluster", "kind": "provisioningcluster", "cluster": "local", "namespace": "fleet-default"}
	}]`, text.Text)
}

func TestScaleNodePoolPlanAutoscaledPool(t *testing.T) {
	tools := newTestProvisioningTools(newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{{
		Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2),
		AutoscalingMinSize: ptr.To[int32](1), AutoscalingMaxSize: ptr.To[int32](5),
	}}))

	_, _, err := tools.scaleClusterNodePoolPlan(middleware.WithToken(t.Context(), testToken), newToolRequest("scaleClusterNodePoolPlan"),
		scaleNodePoolParameters{Cluster: "test-cluster", Namespace: "fleet-default", NodePoolName: "worker", DesiredSize: 3})

	// The Plan variant of the autoscaling tool is suggested when planning.
	assert.EqualError(t, err, "node pool worker is managed by the cluster autoscaler, which scales it between 1 and 5 nodes, so it cannot be scaled manually. Use updateNodePoolAutoscalingPlan to change its minimum and maximum size, or to disable autoscaling for the node pool")
}
