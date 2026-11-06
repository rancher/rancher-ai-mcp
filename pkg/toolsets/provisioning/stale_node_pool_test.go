package provisioning

import (
	"errors"
	"fmt"
	"net/http"

	"testing"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

var provisioningClusterGVR = schema.GroupVersionResource{Group: "provisioning.cattle.io", Version: "v1", Resource: "clusters"}

// newConcurrentChangeTools returns Tools for a cluster with the given machine pools, where the machine pools
// are replaced with changedPools after they have been read, just before the patch is applied.
// This simulates a concurrent change, e.g. by the cluster autoscaler, or a plan that is applied after the cluster changed.
// If apiServerErrors is true, patches that can't be applied fail with the generic 422 status returned by the API server,
// which doesn't include the JSON patch error, instead of the JSON patch error returned by the fake client.
func newConcurrentChangeTools(t *testing.T, pools, changedPools []provisioningV1.RKEMachinePool, apiServerErrors bool) *Tools {
	t.Helper()

	fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
		newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", pools))
	fakeDynClient.PrependReactor("patch", "clusters", func(action k8stesting.Action) (bool, runtime.Object, error) {
		changed := newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", changedPools)
		require.NoError(t, fakeDynClient.Tracker().Update(provisioningClusterGVR, changed, "fleet-default"))

		if apiServerErrors {
			patch, err := jsonpatch.DecodePatch(action.(k8stesting.PatchAction).GetPatch())
			require.NoError(t, err)
			changedBytes, err := changed.MarshalJSON()
			require.NoError(t, err)
			if _, err := patch.Apply(changedBytes); err != nil {
				return true, nil, apiServerPatchError(err)
			}
		}
		// Let the default reactor apply the patch to the changed cluster.
		return false, nil, nil
	})

	return newTestTools(fakeDynClient)
}

// apiServerPatchError returns the error the API server returns when a JSON patch can't be applied.
func apiServerPatchError(err error) error {
	return apierrors.NewGenericServerResponse(http.StatusUnprocessableEntity, "", schema.GroupResource{}, "", err.Error(), 0, false)
}

func TestUpdateNodePoolAutoscalingConcurrentChange(t *testing.T) {
	tests := map[string]struct {
		pools         []provisioningV1.RKEMachinePool
		changedPools  []provisioningV1.RKEMachinePool
		params        updateNodePoolAutoscalingParameters
		expectedError string
	}{
		"pool resized after it was read": {
			pools: []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			changedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](4),
			}},
			// The pool is resized from 2 to 3 nodes, which is based on the size that was read.
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 3, MaxSize: 6},
			expectedError: "node pool worker changed after it was read, get its current configuration and try again",
		},
		"autoscaling range changed after it was read": {
			pools: []provisioningV1.RKEMachinePool{testEtcdPool, testAutoscaledWorkerPool},
			changedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2),
				AutoscalingMinSize: ptr.To[int32](1), AutoscalingMaxSize: ptr.To[int32](10),
			}},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 2, MaxSize: 4},
			expectedError: "node pool worker changed after it was read, get its current configuration and try again",
		},
		"pools reordered after they were read": {
			pools:         []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			changedPools:  []provisioningV1.RKEMachinePool{testWorkerPool, testEtcdPool},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 5},
			expectedError: "node pool worker changed after it was read, get its current configuration and try again",
		},
		"autoscaling range changed before it was disabled": {
			pools: []provisioningV1.RKEMachinePool{testEtcdPool, testAutoscaledWorkerPool},
			changedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2),
				AutoscalingMinSize: ptr.To[int32](1), AutoscalingMaxSize: ptr.To[int32](10),
			}},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", Disable: true},
			expectedError: "node pool worker changed after it was read, get its current configuration and try again",
		},
		"autoscaling enabled by someone else after the pool was read": {
			pools: []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			changedPools: []provisioningV1.RKEMachinePool{testEtcdPool, {
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2),
				AutoscalingMinSize: ptr.To[int32](2), AutoscalingMaxSize: ptr.To[int32](8),
			}},
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 1, MaxSize: 5},
			expectedError: "node pool worker changed after it was read, get its current configuration and try again",
		},
		"unset quantity set after it was read": {
			pools:        []provisioningV1.RKEMachinePool{{Name: "worker", WorkerRole: true}},
			changedPools: []provisioningV1.RKEMachinePool{{Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](3)}},
			// The pool is resized from 1 to 2 nodes, based on the quantity not being set.
			params:        updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 2, MaxSize: 4},
			expectedError: "node pool worker changed after it was read, get its current configuration and try again",
		},
		"another pool changed after it was read": {
			pools: []provisioningV1.RKEMachinePool{testEtcdPool, testWorkerPool},
			changedPools: []provisioningV1.RKEMachinePool{{
				Name: "cp-etcd", EtcdRole: true, ControlPlaneRole: true, Quantity: ptr.To[int32](5),
			}, testWorkerPool},
			params: updateNodePoolAutoscalingParameters{Cluster: "test-cluster", NodePoolName: "worker", MinSize: 3, MaxSize: 6},
		},
	}

	for name, tt := range tests {
		for _, apiServerErrors := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s (API server errors: %t)", name, apiServerErrors), func(t *testing.T) {
				tools := newConcurrentChangeTools(t, tt.pools, tt.changedPools, apiServerErrors)

				_, _, err := tools.updateNodePoolAutoscaling(middleware.WithToken(t.Context(), testToken), newToolRequest("updateNodePoolAutoscaling"), tt.params)

				if tt.expectedError != "" {
					assert.ErrorContains(t, err, tt.expectedError)
					return
				}
				assert.NoError(t, err)
			})
		}
	}
}

func TestScaleClusterNodePoolConcurrentChange(t *testing.T) {
	pool := provisioningV1.RKEMachinePool{Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2)}

	tests := map[string]struct {
		changedPools  []provisioningV1.RKEMachinePool
		expectedError string
	}{
		"pool resized after it was read": {
			changedPools:  []provisioningV1.RKEMachinePool{{Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](3)}},
			expectedError: "node pool worker changed after it was read, get its current configuration and try again",
		},
		"autoscaling enabled after it was read": {
			changedPools: []provisioningV1.RKEMachinePool{{
				Name: "worker", WorkerRole: true, Quantity: ptr.To[int32](2),
				AutoscalingMinSize: ptr.To[int32](1), AutoscalingMaxSize: ptr.To[int32](5),
			}},
			expectedError: "node pool worker changed after it was read, get its current configuration and try again",
		},
		"pool unchanged": {
			changedPools: []provisioningV1.RKEMachinePool{pool},
		},
	}

	for name, tt := range tests {
		for _, apiServerErrors := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s (API server errors: %t)", name, apiServerErrors), func(t *testing.T) {
				tools := newConcurrentChangeTools(t, []provisioningV1.RKEMachinePool{pool}, tt.changedPools, apiServerErrors)
				// Adding a node is based on the size that was read.
				_, _, err := tools.scaleClusterNodePool(middleware.WithToken(t.Context(), testToken), newToolRequest("scaleClusterNodePool"),
					scaleNodePoolParameters{Cluster: "test-cluster", Namespace: "fleet-default", NodePoolName: "worker", AmountToAdd: 1})

				if tt.expectedError != "" {
					assert.ErrorContains(t, err, tt.expectedError)
					return
				}
				assert.NoError(t, err)
			})
		}
	}
}

func TestStaleNodePoolUnrelatedErrors(t *testing.T) {
	validationErr := apierrors.NewInvalid(schema.GroupKind{Group: "provisioning.cattle.io", Kind: "Cluster"}, "test-cluster", field.ErrorList{
		field.Invalid(field.NewPath("spec", "rkeConfig"), "", "invalid value"),
	})

	tests := map[string]error{
		// The pool is unchanged, so the patch tests pass against the current cluster.
		"generic 422 status for an unchanged pool": apiServerPatchError(errors.New("replace operation does not apply: doc is missing key")),
		"validation error":                         validationErr,
		"other error":                              errors.New("connection refused: test failed"),
	}

	for name, patchErr := range tests {
		t.Run(name, func(t *testing.T) {
			fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
				newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{testWorkerPool}))
			fakeDynClient.PrependReactor("patch", "clusters", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, patchErr
			})
			tools := newTestTools(fakeDynClient)

			_, _, err := tools.scaleClusterNodePool(middleware.WithToken(t.Context(), testToken), newToolRequest("scaleClusterNodePool"),
				scaleNodePoolParameters{Cluster: "test-cluster", Namespace: "fleet-default", NodePoolName: "worker", AmountToAdd: 1})

			assert.Same(t, patchErr, err)
		})
	}
}

func TestPatchTestsFail(t *testing.T) {
	obj := newProvisioningClusterWithRKEConfig("test-cluster", "fleet-default", "c-m-abc123", []provisioningV1.RKEMachinePool{testWorkerPool})

	tests := map[string]struct {
		patch    string
		expected bool
	}{
		"passing tests": {
			patch:    `[{"op": "test", "path": "/spec/rkeConfig/machinePools/0/quantity", "value": 2}, {"op": "replace", "path": "/spec/rkeConfig/machinePools/0/quantity", "value": 3}]`,
			expected: false,
		},
		"failing test": {
			patch:    `[{"op": "test", "path": "/spec/rkeConfig/machinePools/0/quantity", "value": 4}]`,
			expected: true,
		},
		"field set that was expected to be unset": {
			patch:    `[{"op": "test", "path": "/spec/rkeConfig/machinePools/0/name", "value": null}]`,
			expected: true,
		},
		"only failing non-test operations": {
			patch:    `[{"op": "remove", "path": "/spec/missing"}]`,
			expected: false,
		},
		"invalid patch": {
			patch:    `not json`,
			expected: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expected, patchTestsFail(obj, []byte(tt.patch)))
		})
	}
}
