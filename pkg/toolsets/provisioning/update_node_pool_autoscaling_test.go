package provisioning

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

var (
	testEtcdPool = provisioningV1.RKEMachinePool{
		Name:             "cp-etcd",
		EtcdRole:         true,
		ControlPlaneRole: true,
		Quantity:         ptr.To[int32](3),
	}
	testWorkerPool = provisioningV1.RKEMachinePool{
		Name:       "worker",
		WorkerRole: true,
		Quantity:   ptr.To[int32](2),
	}
	testAutoscaledWorkerPool = provisioningV1.RKEMachinePool{
		Name:               "worker",
		WorkerRole:         true,
		Quantity:           ptr.To[int32](2),
		AutoscalingMinSize: ptr.To[int32](1),
		AutoscalingMaxSize: ptr.To[int32](5),
	}
)

func TestUpdateNodePoolAutoscalingPlan(t *testing.T) {
	tests := map[string]struct {
		pools         []provisioningV1.RKEMachinePool
		params        updateNodePoolAutoscalingParameters
		expectedPatch string
		expectedError string
	}{
		"enable autoscaling with the current size within the range": {
			pools:  []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 5},
			expectedPatch: `[
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/name", "value": "worker"},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/autoscalingMinSize", "value": null},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/autoscalingMaxSize", "value": null},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/1/autoscalingMinSize", "value": 1},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/1/autoscalingMaxSize", "value": 5}
			]`,
		},
		"enable autoscaling with the current size below the range": {
			pools:  []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 3, MaxSize: 5},
			expectedPatch: `[
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/name", "value": "worker"},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/autoscalingMinSize", "value": null},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/autoscalingMaxSize", "value": null},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/quantity", "value": 2},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/1/autoscalingMinSize", "value": 3},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/1/autoscalingMaxSize", "value": 5},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/1/quantity", "value": 3}
			]`,
		},
		"change autoscaling with the current size above the range": {
			pools:  []provisioningV1.RKEMachinePool{testEtcdPool, testAutoscaledWorkerPool},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 1},
			expectedPatch: `[
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/name", "value": "worker"},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/autoscalingMinSize", "value": 1},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/autoscalingMaxSize", "value": 5},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/quantity", "value": 2},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/1/autoscalingMinSize", "value": 1},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/1/autoscalingMaxSize", "value": 1},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/1/quantity", "value": 1}
			]`,
		},
		"enable autoscaling for a pool without a quantity": {
			pools:  []provisioningV1.RKEMachinePool{{Name: "worker", WorkerRole: true}},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 2, MaxSize: 4},
			expectedPatch: `[
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/name", "value": "worker"},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize", "value": null},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize", "value": null},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/quantity", "value": null},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize", "value": 2},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize", "value": 4},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/0/quantity", "value": 2}
			]`,
		},
		"enable autoscaling for a control plane pool": {
			pools:  []provisioningV1.RKEMachinePool{{Name: "cp", ControlPlaneRole: true, Quantity: ptr.To[int32](2)}},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "cp", MinSize: 2, MaxSize: 3},
			expectedPatch: `[
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/name", "value": "cp"},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize", "value": null},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize", "value": null},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize", "value": 2},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize", "value": 3}
			]`,
		},
		"node pool name prefixed with the cluster name": {
			pools:  []provisioningV1.RKEMachinePool{testWorkerPool},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "test-cluster-worker", MinSize: 1, MaxSize: 5},
			expectedPatch: `[
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/name", "value": "worker"},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize", "value": null},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize", "value": null},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize", "value": 1},
				{"op": "add", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize", "value": 5}
			]`,
		},
		"disable autoscaling": {
			pools:  []provisioningV1.RKEMachinePool{testEtcdPool, testAutoscaledWorkerPool},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", Disable: true},
			expectedPatch: `[
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/name", "value": "worker"},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/autoscalingMinSize", "value": 1},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/1/autoscalingMaxSize", "value": 5},
				{"op": "remove", "path": "/spec/rkeConfig/machinePools/1/autoscalingMinSize"},
				{"op": "remove", "path": "/spec/rkeConfig/machinePools/1/autoscalingMaxSize"}
			]`,
		},
		"disable autoscaling for an etcd pool": {
			pools: []provisioningV1.RKEMachinePool{{
				Name: "cp-etcd", EtcdRole: true, ControlPlaneRole: true, Quantity: ptr.To[int32](3),
				AutoscalingMinSize: ptr.To[int32](3), AutoscalingMaxSize: ptr.To[int32](5),
			}},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "cp-etcd", Disable: true},
			expectedPatch: `[
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/name", "value": "cp-etcd"},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize", "value": 3},
				{"op": "test", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize", "value": 5},
				{"op": "remove", "path": "/spec/rkeConfig/machinePools/0/autoscalingMinSize"},
				{"op": "remove", "path": "/spec/rkeConfig/machinePools/0/autoscalingMaxSize"}
			]`,
		},
		"refuse to enable autoscaling for an etcd pool": {
			pools:         []provisioningV1.RKEMachinePool{testEtcdPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "cp-etcd", MinSize: 3, MaxSize: 5},
			expectedError: "autoscaling is not supported for etcd node pools, as the cluster autoscaler can scale them to an even number of nodes, risking loss of etcd quorum. Use scaleClusterNodePoolPlan to change the size of etcd node pools",
		},
		"disable autoscaling when it is not enabled": {
			pools:         []provisioningV1.RKEMachinePool{testWorkerPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", Disable: true},
			expectedError: "autoscaling is not enabled for node pool worker",
		},
		"disable autoscaling with sizes": {
			pools:         []provisioningV1.RKEMachinePool{testAutoscaledWorkerPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", Disable: true, MinSize: 1},
			expectedError: "minSize and maxSize cannot be specified when disabling autoscaling",
		},
		"minimum size of zero": {
			pools:         []provisioningV1.RKEMachinePool{testWorkerPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MaxSize: 5},
			expectedError: "minSize must be at least 1",
		},
		"maximum size below the minimum size": {
			pools:         []provisioningV1.RKEMachinePool{testWorkerPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 3, MaxSize: 2},
			expectedError: "maxSize (2) must be greater than or equal to minSize (3)",
		},
		"maximum size above the int32 range": {
			pools:         []provisioningV1.RKEMachinePool{testWorkerPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: math.MaxInt32 + 1},
			expectedError: "maxSize (2147483648) is out of range",
		},
		"minimum size that would wrap around to a valid size": {
			pools:         []provisioningV1.RKEMachinePool{testWorkerPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1<<32 + 1, MaxSize: 1<<32 + 5},
			expectedError: "minSize (4294967297) is out of range",
		},
		"node pool not found": {
			pools:         []provisioningV1.RKEMachinePool{testWorkerPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "missing", MinSize: 1, MaxSize: 5},
			expectedError: "node pool missing not found in cluster test-cluster",
		},
		"cluster without node pools": {
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 5},
			expectedError: "cluster test-cluster has no node pools",
		},
		"local cluster": {
			params:        updateNodePoolAutoscalingParameters{Cluster: "local", NodePoolName: "worker", MinSize: 1, MaxSize: 5},
			expectedError: "autoscaling node pools in the local cluster is not supported",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := newTestProvisioningTools(newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", tt.pools))

			result, _, err := tools.updateNodePoolAutoscalingPlan(middleware.WithToken(t.Context(), testToken), newToolRequest("updateNodePoolAutoscalingPlan"), tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected type *mcp.TextContent")
			assert.JSONEq(t, `[{
				"type": "update",
				"payload": `+tt.expectedPatch+`,
				"resource": {"name": "test-cluster", "kind": "provisioningcluster", "cluster": "local", "namespace": "fleet-default"}
			}]`, text.Text)
		})
	}
}

func TestUpdateNodePoolAutoscaling(t *testing.T) {
	tests := map[string]struct {
		pools         []provisioningV1.RKEMachinePool
		annotations   map[string]string
		objects       []runtime.Object
		params        updateNodePoolAutoscalingParameters
		expectedPools []provisioningV1.RKEMachinePool
		expectedNote  string
	}{
		"enable autoscaling and resize the node pool into the range": {
			pools:  []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 3, MaxSize: 5},
			expectedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](3),
				AutoscalingMinSize: ptr.To[int32](3), AutoscalingMaxSize: ptr.To[int32](5),
			}},
			expectedNote: "node pool worker was resized from 2 to 3 nodes to fit within the new autoscaling range of 3 to 5 nodes",
		},
		"change autoscaling within the range": {
			pools:  []provisioningV1.RKEMachinePool{testEtcdPool, testAutoscaledWorkerPool},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 2, MaxSize: 10},
			expectedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2),
				AutoscalingMinSize: ptr.To[int32](2), AutoscalingMaxSize: ptr.To[int32](10),
			}},
		},
		"disable autoscaling": {
			pools:         []provisioningV1.RKEMachinePool{testEtcdPool, testAutoscaledWorkerPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", Disable: true},
			expectedPools: []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
		},
		"disable a partial autoscaling configuration": {
			pools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2), AutoscalingMinSize: ptr.To[int32](1),
			}},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", Disable: true},
			expectedPools: []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
		},
		"enable autoscaling with the feature enabled": {
			pools:   []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			objects: []runtime.Object{newFeature("cluster-autoscaling", new(true), false)},
			params:  updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 3},
			expectedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2),
				AutoscalingMinSize: ptr.To[int32](1), AutoscalingMaxSize: ptr.To[int32](3),
			}},
		},
		"enable autoscaling with the feature disabled": {
			pools:   []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			objects: []runtime.Object{newFeature("cluster-autoscaling", nil, false)},
			params:  updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 3},
			expectedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2),
				AutoscalingMinSize: ptr.To[int32](1), AutoscalingMaxSize: ptr.To[int32](3),
			}},
			expectedNote: "the cluster-autoscaling feature is disabled in Rancher, so the cluster autoscaler won't scale node pool worker until a Rancher administrator enables the feature",
		},
		"enable autoscaling with the autoscaler paused and a resize": {
			pools:       []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			annotations: map[string]string{"provisioning.cattle.io/cluster-autoscaler-paused": "true"},
			params:      updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 1},
			expectedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](1),
				AutoscalingMinSize: ptr.To[int32](1), AutoscalingMaxSize: ptr.To[int32](1),
			}},
			expectedNote: "node pool worker was resized from 2 to 1 nodes to fit within the new autoscaling range of 1 to 1 nodes\n" +
				"the cluster autoscaler is paused for cluster test-cluster by the provisioning.cattle.io/cluster-autoscaler-paused annotation, so it won't scale node pool worker until it is resumed",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", tt.pools)
			cluster.SetAnnotations(tt.annotations)
			tools := newTestProvisioningTools(append([]runtime.Object{cluster}, tt.objects...)...)

			result, _, err := tools.updateNodePoolAutoscaling(middleware.WithToken(t.Context(), testToken), newToolRequest("updateNodePoolAutoscaling"), tt.params)
			require.NoError(t, err)

			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected type *mcp.TextContent")

			// The resources are wrapped with a note when the node pool was resized.
			var resp struct {
				LLM json.RawMessage `json:"llm"`
			}
			require.NoError(t, json.Unmarshal([]byte(text.Text), &resp))
			var clusters []provisioningV1.Cluster
			var note string
			if bytes.HasPrefix(bytes.TrimSpace(resp.LLM), []byte("[")) {
				require.NoError(t, json.Unmarshal(resp.LLM, &clusters))
			} else {
				var withNote struct {
					Resources []provisioningV1.Cluster `json:"resources"`
					Note      string                   `json:"note"`
				}
				require.NoError(t, json.Unmarshal(resp.LLM, &withNote))
				clusters, note = withNote.Resources, withNote.Note
			}

			require.Len(t, clusters, 1)
			assert.Equal(t, tt.expectedPools, clusters[0].Spec.RKEConfig.MachinePools)
			assert.Equal(t, tt.expectedNote, note)
		})
	}
}

func TestUpdateNodePoolAutoscalingResponse(t *testing.T) {
	pool := testWorkerPool
	pool.DynamicSchemaSpec = `{"resourceFields":{"region":{"type":"string"}}}`
	tools := newTestProvisioningTools(newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{pool}))

	result, _, err := tools.updateNodePoolAutoscaling(middleware.WithToken(t.Context(), testToken), newToolRequest("updateNodePoolAutoscaling"),
		updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 5})
	require.NoError(t, err)

	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected type *mcp.TextContent")
	assert.NotContains(t, text.Text, "dynamicSchemaSpec")

	var resp struct {
		UIContext []map[string]string `json:"uiContext"`
	}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &resp))
	assert.Equal(t, []map[string]string{{
		"namespace": "fleet-default",
		"kind":      "Cluster",
		"cluster":   "local",
		"name":      "test-cluster",
		"type":      "provisioning.cattle.io.cluster",
	}}, resp.UIContext)
}
