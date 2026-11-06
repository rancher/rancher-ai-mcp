package provisioning

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

func TestGetClusterAutoscaling(t *testing.T) {
	controlPlanePool := provisioningV1.RKEMachinePool{
		Name:             "control-plane",
		EtcdRole:         true,
		ControlPlaneRole: true,
		Quantity:         ptr.To[int32](3),
	}
	autoscaledWorkerPool := provisioningV1.RKEMachinePool{
		Name:               "worker",
		WorkerRole:         true,
		Quantity:           ptr.To[int32](2),
		AutoscalingMinSize: ptr.To[int32](1),
		AutoscalingMaxSize: ptr.To[int32](5),
	}
	workerPool := provisioningV1.RKEMachinePool{
		Name:       "worker",
		WorkerRole: true,
		Quantity:   ptr.To[int32](2),
	}

	uiContext := `"uiContext": [{
		"namespace": "fleet-default",
		"kind": "Cluster",
		"cluster": "local",
		"name": "test-cluster",
		"type": "provisioning.cattle.io.cluster"
	}]`

	tests := map[string]struct {
		params         inspectClusterParams
		objects        []runtime.Object
		expectedResult string
		expectedError  string
	}{
		"autoscaling enabled with a ready deployment": {
			params: inspectClusterParams{Cluster: "test-cluster"},
			objects: []runtime.Object{
				newAutoscalingProvisioningCluster("test-cluster", nil,
					[]genericcondition.GenericCondition{
						{Type: "Ready", Status: corev1.ConditionTrue},
						{Type: "ClusterAutoscalerDeploymentReady", Status: corev1.ConditionTrue},
					},
					controlPlanePool, autoscaledWorkerPool),
				newFeature("cluster-autoscaling", nil, true),
			},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"namespace": "fleet-default",
					"supported": true,
					"featureEnabled": true,
					"enabled": true,
					"paused": false,
					"deployment": {"ready": "True"},
					"nodePools": [
						{"name": "control-plane", "roles": ["control-plane", "etcd"], "quantity": 3, "autoscaled": false},
						{"name": "worker", "roles": ["worker"], "quantity": 2, "autoscaled": true, "minSize": 1, "maxSize": 5}
					]
				},
				` + uiContext + `
			}`,
		},
		"autoscaling paused with the feature disabled": {
			params: inspectClusterParams{Cluster: "test-cluster", Namespace: "fleet-default"},
			objects: []runtime.Object{
				newAutoscalingProvisioningCluster("test-cluster",
					map[string]string{"provisioning.cattle.io/cluster-autoscaler-paused": "true"},
					[]genericcondition.GenericCondition{
						{Type: "ClusterAutoscalerDeploymentReady", Status: corev1.ConditionFalse, Message: "[Waiting] autoscaler deployment pending"},
					},
					controlPlanePool, autoscaledWorkerPool),
				newFeature("cluster-autoscaling", new(false), true),
			},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"namespace": "fleet-default",
					"supported": true,
					"featureEnabled": false,
					"enabled": true,
					"paused": true,
					"deployment": {"ready": "False", "message": "[Waiting] autoscaler deployment pending"},
					"nodePools": [
						{"name": "control-plane", "roles": ["control-plane", "etcd"], "quantity": 3, "autoscaled": false},
						{"name": "worker", "roles": ["worker"], "quantity": 2, "autoscaled": true, "minSize": 1, "maxSize": 5}
					]
				},
				` + uiContext + `
			}`,
		},
		"no autoscaled node pools": {
			params: inspectClusterParams{Cluster: "test-cluster"},
			objects: []runtime.Object{
				newAutoscalingProvisioningCluster("test-cluster", nil, nil, controlPlanePool, workerPool),
				newFeature("cluster-autoscaling", new(true), false),
			},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"namespace": "fleet-default",
					"supported": true,
					"featureEnabled": true,
					"enabled": false,
					"paused": false,
					"nodePools": [
						{"name": "control-plane", "roles": ["control-plane", "etcd"], "quantity": 3, "autoscaled": false},
						{"name": "worker", "roles": ["worker"], "quantity": 2, "autoscaled": false}
					]
				},
				` + uiContext + `
			}`,
		},
		"node pools with partial autoscaling configurations": {
			params: inspectClusterParams{Cluster: "test-cluster"},
			objects: []runtime.Object{
				newAutoscalingProvisioningCluster("test-cluster", nil, nil,
					provisioningV1.RKEMachinePool{Name: "min-only", WorkerRole: true, Quantity: ptr.To[int32](2), AutoscalingMinSize: ptr.To[int32](1)},
					provisioningV1.RKEMachinePool{Name: "max-only", WorkerRole: true, Quantity: ptr.To[int32](2), AutoscalingMaxSize: ptr.To[int32](5)},
				),
				newFeature("cluster-autoscaling", new(true), false),
			},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"namespace": "fleet-default",
					"supported": true,
					"featureEnabled": true,
					"enabled": false,
					"paused": false,
					"nodePools": [
						{
							"name": "min-only", "roles": ["worker"], "quantity": 2, "autoscaled": false, "minSize": 1,
							"warning": "autoscaling is not enabled because maxSize is not set, the cluster autoscaler requires both minSize and maxSize. Use updateNodePoolAutoscaling to set both, or to disable autoscaling and remove the partial configuration"
						},
						{
							"name": "max-only", "roles": ["worker"], "quantity": 2, "autoscaled": false, "maxSize": 5,
							"warning": "autoscaling is not enabled because minSize is not set, the cluster autoscaler requires both minSize and maxSize. Use updateNodePoolAutoscaling to set both, or to disable autoscaling and remove the partial configuration"
						}
					]
				},
				` + uiContext + `
			}`,
		},
		"node pool without quantity reports a single node": {
			params: inspectClusterParams{Cluster: "test-cluster"},
			objects: []runtime.Object{
				newAutoscalingProvisioningCluster("test-cluster", nil, nil,
					provisioningV1.RKEMachinePool{Name: "worker", WorkerRole: true},
				),
				newFeature("cluster-autoscaling", new(true), false),
			},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"namespace": "fleet-default",
					"supported": true,
					"featureEnabled": true,
					"enabled": false,
					"paused": false,
					"nodePools": [
						{"name": "worker", "roles": ["worker"], "quantity": 1, "autoscaled": false}
					]
				},
				` + uiContext + `
			}`,
		},
		"cluster without machine pools and unreadable feature flag": {
			params: inspectClusterParams{Cluster: "test-cluster"},
			objects: []runtime.Object{
				newAutoscalingProvisioningCluster("test-cluster", nil, nil),
			},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"namespace": "fleet-default",
					"supported": false,
					"reason": "the cluster has no machine pools. Autoscaling is only supported for rke2 and k3s clusters provisioned by Rancher with machine pools",
					"enabled": false,
					"paused": false,
					"nodePools": []
				},
				` + uiContext + `
			}`,
		},
		"cluster not found": {
			params:        inspectClusterParams{Cluster: "missing-cluster"},
			objects:       []runtime.Object{},
			expectedError: "provisioning cluster missing-cluster not found in namespace fleet-default",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := newTestProvisioningTools(tt.objects...)

			result, _, err := tools.getClusterAutoscaling(middleware.WithToken(t.Context(), testToken), newToolRequest("getClusterAutoscaling"), tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected type *mcp.TextContent")
			assert.JSONEq(t, tt.expectedResult, text.Text)
		})
	}
}

// newAutoscalingProvisioningCluster creates a test Provisioning Cluster in the fleet-default namespace.
// The RKE config is only set if machine pools are provided.
func newAutoscalingProvisioningCluster(name string, annotations map[string]string, conditions []genericcondition.GenericCondition, machinePools ...provisioningV1.RKEMachinePool) *unstructured.Unstructured {
	cluster := &provisioningV1.Cluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "provisioning.cattle.io/v1",
			Kind:       "Cluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "fleet-default",
			Annotations: annotations,
		},
		Status: provisioningV1.ClusterStatus{
			Conditions: conditions,
		},
	}
	if len(machinePools) > 0 {
		cluster.Spec.RKEConfig = &provisioningV1.RKEConfig{
			MachinePools: machinePools,
		}
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cluster)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: obj}
}

// newFeature creates a test management.cattle.io/v3 Feature. A nil value means the feature uses its default.
func newFeature(name string, value *bool, defaultValue bool) *unstructured.Unstructured {
	var specValue any
	if value != nil {
		specValue = *value
	}

	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Feature",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"value": specValue,
			},
			"status": map[string]any{
				"default": defaultValue,
				"dynamic": false,
			},
		},
	}
}
