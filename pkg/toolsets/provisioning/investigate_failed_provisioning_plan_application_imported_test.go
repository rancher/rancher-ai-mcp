package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher/pkg/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testImportedNode    = "imported-node-1"
	testImportedMachine = "imported-machine"
)

// newClusterWithProvider creates a management cluster with the given machine provider and annotations.
func newClusterWithProvider(id, machineProvider string, annotations map[string]interface{}) *unstructured.Unstructured {
	cluster := newManagementCluster(id, true)
	_ = unstructured.SetNestedField(cluster.Object, machineProvider, "status", "info", "machineProvider")
	if annotations != nil {
		_ = unstructured.SetNestedMap(cluster.Object, annotations, "metadata", "annotations")
	}

	return cluster
}

// newImportedCluster creates an imported management cluster, optionally with the day two operations annotation.
func newImportedCluster(id, operationsEnabled string) *unstructured.Unstructured {
	var annotations map[string]interface{}
	if operationsEnabled != "" {
		annotations = map[string]interface{}{operationsEnabledAnnotation: operationsEnabled}
	}

	return newClusterWithProvider(id, importedMachineProvider, annotations)
}

// newTestFeature creates a Rancher feature which is set to the given value.
func newTestFeature(name string, enabled bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Feature",
		"metadata":   map[string]interface{}{"name": name},
		"spec":       map[string]interface{}{"value": enabled},
	}}
}

// newTestSetting creates a Rancher setting.
func newTestSetting(name, value, defaultValue string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Setting",
		"metadata":   map[string]interface{}{"name": name},
		"value":      value,
		"default":    defaultValue,
	}}
}

// day2OpsEnabled returns the feature and setting which enable day two operations on imported clusters by default.
func day2OpsEnabled() []runtime.Object {
	return []runtime.Object{
		newTestFeature(importedDay2OpsFeature, true),
		newTestSetting(importedDay2OpsSetting, "true", "false"),
	}
}

// newImportedPlanSecret creates a machine plan secret as it is stored for a node of an imported cluster.
func newImportedPlanSecret(secretName, clusterID, nodeName, secretType string, data map[string][]byte) *unstructured.Unstructured {
	secret := &corev1.Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Name:       secretName,
		Namespace:  clusterID,
		Labels: map[string]string{
			importedPlanClusterLabel: clusterID,
			importedPlanNodeLabel:    nodeName,
		},
		Type: corev1.SecretType(secretType),
		Data: data,
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
	if err != nil {
		panic(err)
	}

	return &unstructured.Unstructured{Object: obj}
}

func withObjects(base []runtime.Object, objects ...runtime.Object) []runtime.Object {
	return append(append([]runtime.Object{}, base...), objects...)
}

func TestCheckImportedDay2Ops(t *testing.T) {
	tests := map[string]struct {
		objects       []runtime.Object
		cluster       *unstructured.Unstructured
		expectedError string
	}{
		"feature does not exist": {
			objects:       []runtime.Object{newTestSetting(importedDay2OpsSetting, "true", "")},
			cluster:       newImportedCluster("c-feature-missing", "true"),
			expectedError: "the imported-day-2-ops feature is not enabled",
		},
		"feature disabled ignores the setting and annotation": {
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, false),
				newTestSetting(importedDay2OpsSetting, "true", ""),
			},
			cluster:       newImportedCluster("c-feature-disabled", "true"),
			expectedError: "the imported-day-2-ops feature is not enabled",
		},
		"setting does not exist ignores the annotation": {
			objects:       []runtime.Object{newTestFeature(importedDay2OpsFeature, true)},
			cluster:       newImportedCluster("c-setting-missing", "true"),
			expectedError: "the imported-cluster-day2-ops-enabled setting does not exist",
		},
		"setting enabled without an annotation": {
			objects: day2OpsEnabled(),
			cluster: newImportedCluster("c-setting-enabled", ""),
		},
		"setting enabled by its default": {
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, true),
				newTestSetting(importedDay2OpsSetting, "", "true"),
			},
			cluster: newImportedCluster("c-setting-default", ""),
		},
		"setting value is compared exactly": {
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, true),
				newTestSetting(importedDay2OpsSetting, " TRUE ", ""),
			},
			cluster:       newImportedCluster("c-setting-case", ""),
			expectedError: "day two operations are not enabled for imported cluster c-setting-case",
		},
		"whitespace setting value does not fall back to an enabled default": {
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, true),
				newTestSetting(importedDay2OpsSetting, "   ", "true"),
			},
			cluster:       newImportedCluster("c-setting-whitespace", ""),
			expectedError: "day two operations are not enabled for imported cluster c-setting-whitespace",
		},
		"setting disabled without an annotation": {
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, true),
				newTestSetting(importedDay2OpsSetting, "false", "true"),
			},
			cluster:       newImportedCluster("c-setting-disabled", ""),
			expectedError: "day two operations are not enabled for imported cluster c-setting-disabled",
		},
		"annotation enables a cluster when the setting is disabled": {
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, true),
				newTestSetting(importedDay2OpsSetting, "false", ""),
			},
			cluster: newImportedCluster("c-annotation-true", "true"),
		},
		"annotation is compared exactly and defers to a disabled setting": {
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, true),
				newTestSetting(importedDay2OpsSetting, "false", ""),
			},
			cluster:       newImportedCluster("c-annotation-case", " True "),
			expectedError: "day two operations are not enabled for imported cluster c-annotation-case",
		},
		"annotation disables a cluster when the setting is enabled": {
			objects:       day2OpsEnabled(),
			cluster:       newImportedCluster("c-annotation-false", "false"),
			expectedError: "disabled for imported cluster c-annotation-false by its rancher.io/operations-enabled annotation",
		},
		"annotation false is compared exactly and defers to an enabled setting": {
			objects: day2OpsEnabled(),
			cluster: newImportedCluster("c-annotation-false-case", "FALSE"),
		},
		"unrecognized annotation falls back to an enabled setting": {
			objects: day2OpsEnabled(),
			cluster: newImportedCluster("c-annotation-unknown-enabled", "maybe"),
		},
		"unrecognized annotation falls back to a disabled setting": {
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, true),
				newTestSetting(importedDay2OpsSetting, "false", ""),
			},
			cluster:       newImportedCluster("c-annotation-unknown-disabled", "maybe"),
			expectedError: "day two operations are not enabled for imported cluster c-annotation-unknown-disabled",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := newTestTools(newPlanApplicationDynClient(tt.objects...))

			err := checkImportedDay2Ops(middleware.WithToken(t.Context(), testToken), zap.NewNop(), tools.client, tt.cluster)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}

			assert.NoError(t, err)
		})
	}
}

func TestCheckImportedDay2OpsLookupErrors(t *testing.T) {
	tests := map[string]struct {
		resource      string
		expectedError string
	}{
		"feature lookup fails": {
			resource:      "features",
			expectedError: "failed to get feature imported-day-2-ops",
		},
		"setting lookup fails": {
			resource:      "settings",
			expectedError: "failed to get setting imported-cluster-day2-ops-enabled",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dynClient := newPlanApplicationDynClient(day2OpsEnabled()...)
			dynClient.PrependReactor("get", tt.resource, func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("rancher is unavailable")
			})
			tools := newTestTools(dynClient)

			err := checkImportedDay2Ops(middleware.WithToken(t.Context(), testToken), zap.NewNop(), tools.client, newImportedCluster("c-lookup-error", "true"))

			assert.ErrorContains(t, err, tt.expectedError)
			assert.ErrorContains(t, err, "rancher is unavailable")
		})
	}
}

func TestResolveImportedPlanTarget(t *testing.T) {
	tests := map[string]struct {
		cluster        string
		nodeName       string
		objects        []runtime.Object
		expectedTarget planTarget
		expectedError  string
	}{
		"imported cluster with day two operations": {
			cluster:  "c-resolve-ok",
			nodeName: testImportedNode,
			objects:  withObjects(day2OpsEnabled(), newImportedCluster("c-resolve-ok", "")),
			expectedTarget: planTarget{
				nodeName:            testImportedNode,
				planSecretNamespace: "c-resolve-ok",
				imported:            true,
			},
		},
		"cluster is resolved by its display name": {
			cluster:  "resolve-display-name",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(), func() runtime.Object {
				cluster := newImportedCluster("c-resolve-display", "")
				_ = unstructured.SetNestedField(cluster.Object, "resolve-display-name", "spec", "displayName")
				return cluster
			}()),
			expectedTarget: planTarget{
				nodeName:            testImportedNode,
				planSecretNamespace: "c-resolve-display",
				imported:            true,
			},
		},
		"cluster unknown to rancher": {
			cluster:       "c-resolve-unknown",
			nodeName:      testImportedNode,
			objects:       day2OpsEnabled(),
			expectedError: "failed to resolve cluster c-resolve-unknown",
		},
		"hosted cluster": {
			cluster:       "c-resolve-hosted",
			nodeName:      testImportedNode,
			objects:       withObjects(day2OpsEnabled(), newClusterWithProvider("c-resolve-hosted", "eks", nil)),
			expectedError: `its machine provider is "eks". This tool only supports clusters managed by Rancher machine plans and imported clusters, and cannot be run against hosted clusters`,
		},
		"cluster without a machine provider": {
			cluster:       "c-resolve-no-provider",
			nodeName:      testImportedNode,
			objects:       withObjects(day2OpsEnabled(), newManagementCluster("c-resolve-no-provider", true)),
			expectedError: `its machine provider is ""`,
		},
		"cluster with a machine provider which is not a string": {
			cluster:  "c-resolve-bad-provider",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(), func() runtime.Object {
				cluster := newManagementCluster("c-resolve-bad-provider", true)
				_ = unstructured.SetNestedField(cluster.Object, int64(1), "status", "info", "machineProvider")
				return cluster
			}()),
			expectedError: "failed to read the machine provider of cluster c-resolve-bad-provider",
		},
		"node name is required": {
			cluster:       "c-resolve-no-node",
			nodeName:      "",
			objects:       withObjects(day2OpsEnabled(), newImportedCluster("c-resolve-no-node", "")),
			expectedError: "Kubernetes node name is required for imported clusters",
		},
		"node name of only whitespace is required": {
			cluster:       "c-resolve-blank-node",
			nodeName:      "   ",
			objects:       withObjects(day2OpsEnabled(), newImportedCluster("c-resolve-blank-node", "")),
			expectedError: "Kubernetes node name is required for imported clusters",
		},
		"node name is checked before day two operations": {
			cluster:       "c-resolve-order",
			nodeName:      "",
			objects:       []runtime.Object{newImportedCluster("c-resolve-order", "")},
			expectedError: "Kubernetes node name is required for imported clusters",
		},
		"day two operations are disabled": {
			cluster:       "c-resolve-disabled",
			nodeName:      testImportedNode,
			objects:       withObjects(day2OpsEnabled(), newImportedCluster("c-resolve-disabled", "false")),
			expectedError: "disabled for imported cluster c-resolve-disabled by its rancher.io/operations-enabled annotation",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := newTestTools(newPlanApplicationDynClient(tt.objects...))

			target, err := tools.resolveImportedPlanTarget(middleware.WithToken(t.Context(), testToken), zap.NewNop(), tt.cluster, tt.nodeName)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Equal(t, planTarget{}, target)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedTarget, target)
		})
	}
}

// newImportedTools returns tools for a fake dynamic client containing the given objects.
func newImportedTools(objects ...runtime.Object) (*Tools, *dynamicfake.FakeDynamicClient) {
	objects = append(objects, &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata":   map[string]interface{}{"name": testImportedNode},
	}})
	dynClient := newPlanApplicationDynClient(objects...)
	tools := newTestTools(dynClient)
	tools.toolboxImage = testToolboxImage

	return tools, dynClient
}

func TestInvestigateFailedPlanApplicationImported(t *testing.T) {
	expectedChecksum := plan.Checksum([]byte(testPlan))
	expectedSuccessResult := fmt.Sprintf(`{
		"llm": [
			{"logs": {"journald-reader": "fake logs"}},
			{
				"checksum": %q,
				"checksum-note": %q,
				"failed-checksum": "",
				"failure-count": "3",
				"last-apply-time": "2026-10-20T12:00:00Z",
				"max-failures": "5",
				"plan-revision": "12345",
				"plan-state": "failed",
				"probe-statuses": "{\"kubelet\":{\"healthy\":false}}"
			}
		]
	}`, expectedChecksum, "The checksum is of the current plan. The system-agent has not recorded a failed plan checksum.")

	fallbackResult := `{
		"llm": {
			"note": "Machine plan unavailable: %s. Returned recent system-agent logs without filtering by plan hash.",
			"resources": [{"logs": {"journald-reader": "fake logs"}}]
		}
	}`

	planSecretType := machinePlanSecretType

	tests := map[string]struct {
		cluster        string
		nodeName       string
		objects        []runtime.Object
		cancelCtx      bool
		expectedResult string
		// expectedFilter is true when the job filters the logs on the plan hash
		expectedFilter bool
		expectedError  string
	}{
		"imported cluster with a machine plan": {
			cluster:  "c-imp-plan",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-plan", ""),
				newImportedPlanSecret("plan-1", "c-imp-plan", testImportedNode, planSecretType, fullMachinePlanData()),
				newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedResult: expectedSuccessResult,
			expectedFilter: true,
		},
		"machine plans of other nodes and clusters are ignored": {
			cluster:  "c-imp-others",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-others", ""),
				newImportedPlanSecret("plan-other-node", "c-imp-others", "another-node", planSecretType, map[string][]byte{"plan": []byte(`{"other":"node"}`)}),
				newImportedPlanSecret("plan-other-cluster", "c-imp-another", testImportedNode, planSecretType, map[string][]byte{"plan": []byte(`{"other":"cluster"}`)}),
				newImportedPlanSecret("plan-1", "c-imp-others", testImportedNode, planSecretType, fullMachinePlanData()),
				newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedResult: expectedSuccessResult,
			expectedFilter: true,
		},
		"secrets of another type for the node are skipped": {
			cluster:  "c-imp-type",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-type", ""),
				newImportedPlanSecret("not-a-plan", "c-imp-type", testImportedNode, "Opaque", map[string][]byte{"plan": []byte(`{"not":"a plan"}`)}),
				newImportedPlanSecret("plan-1", "c-imp-type", testImportedNode, planSecretType, fullMachinePlanData()),
				newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedResult: expectedSuccessResult,
			expectedFilter: true,
		},
		"no machine plan for the node falls back to the logs": {
			cluster:  "c-imp-none",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-none", ""),
				newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedResult: fmt.Sprintf(fallbackResult, "machine plan not found for node imported-node-1 in cluster c-imp-none"),
		},
		"only secrets of another type falls back to the logs": {
			cluster:  "c-imp-only-type",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-only-type", ""),
				newImportedPlanSecret("not-a-plan", "c-imp-only-type", testImportedNode, "Opaque", fullMachinePlanData()),
				newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedResult: fmt.Sprintf(fallbackResult, "machine plan not found for node imported-node-1 in cluster c-imp-only-type"),
		},
		"multiple machine plans for the node falls back to the logs": {
			cluster:  "c-imp-multiple",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-multiple", ""),
				newImportedPlanSecret("plan-1", "c-imp-multiple", testImportedNode, planSecretType, fullMachinePlanData()),
				newImportedPlanSecret("plan-2", "c-imp-multiple", testImportedNode, planSecretType, fullMachinePlanData()),
				newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedResult: fmt.Sprintf(fallbackResult, "multiple machine plans found for node imported-node-1 in cluster c-imp-multiple"),
		},
		"machine plan without a plan falls back to the logs": {
			cluster:  "c-imp-empty",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-empty", ""),
				newImportedPlanSecret("plan-1", "c-imp-empty", testImportedNode, planSecretType, map[string][]byte{"plan-state": []byte("failed")}),
				newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedResult: fmt.Sprintf(fallbackResult, "no plan found in machine plan resource"),
		},
		"no machine plan and a cancelled context": {
			cluster:  "c-imp-cancel",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-cancel", "")),
			cancelCtx:     true,
			expectedError: "context canceled",
		},
		"no machine plan and the log gatherer job already exists": {
			cluster:  "c-imp-job",
			nodeName: testImportedNode,
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-imp-job", ""),
				newLogGathererJob(testImportedMachine),
				newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedResult: fmt.Sprintf(fallbackResult, "machine plan not found for node imported-node-1 in cluster c-imp-job"),
		},
		"node name is required": {
			cluster:       "c-imp-no-node",
			objects:       withObjects(day2OpsEnabled(), newImportedCluster("c-imp-no-node", "")),
			expectedError: "Kubernetes node name is required for imported clusters",
		},
		"day two operations feature is disabled": {
			cluster:  "c-imp-disabled",
			nodeName: testImportedNode,
			objects: []runtime.Object{
				newTestFeature(importedDay2OpsFeature, false),
				newTestSetting(importedDay2OpsSetting, "true", ""),
				newImportedCluster("c-imp-disabled", "true"),
			},
			expectedError: "the imported-day-2-ops feature is not enabled",
		},
		"day two operations are disabled for the cluster": {
			cluster:       "c-imp-annotation",
			nodeName:      testImportedNode,
			objects:       withObjects(day2OpsEnabled(), newImportedCluster("c-imp-annotation", "false")),
			expectedError: "disabled for imported cluster c-imp-annotation",
		},
		"hosted cluster": {
			cluster:       "c-imp-hosted",
			nodeName:      testImportedNode,
			objects:       withObjects(day2OpsEnabled(), newClusterWithProvider("c-imp-hosted", "gke", nil)),
			expectedError: "cannot be run against hosted clusters",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, dynClient := newImportedTools(tt.objects...)
			ctx := middleware.WithToken(t.Context(), testToken)
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			result, _, err := tools.investigateFailedPlanApplication(ctx, newToolRequest("investigate-failed-provisioning-plan-application"),
				investigateFailedPlanApplicationParams{
					Cluster:     tt.cluster,
					MachineName: testImportedMachine,
					NodeName:    tt.nodeName,
				})

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected type *mcp.TextContent")
			assert.JSONEq(t, tt.expectedResult, text.Text)

			if tt.expectedFilter {
				verifyLogGathererJob(t, dynClient, testImportedMachine, testImportedNode, expectedChecksum)
			} else {
				verifyUnfilteredLogGathererJob(t, dynClient, testImportedMachine, testImportedNode, defaultLogLineTail)
			}
		})
	}
}

// previewedJob returns the log gatherer job in the response of the plan tool.
func previewedJob(t *testing.T, result *mcp.CallToolResult) batchv1.Job {
	t.Helper()

	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected type *mcp.TextContent")

	var planned []struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	require.NoError(t, json.Unmarshal([]byte(text.Text), &planned))
	require.Len(t, planned, 1)
	assert.Equal(t, "create", planned[0].Type)

	var job batchv1.Job
	require.NoError(t, json.Unmarshal(planned[0].Payload, &job))

	return job
}

func TestInvestigateFailedPlanApplicationPlanPlanAvailability(t *testing.T) {
	const unfilteredScript = "echo 'Machine plan unavailable; showing recent system-agent logs'; journalctl -u rancher-system-agent.service --no-pager -o cat | tail -n 150"

	planSecretType := machinePlanSecretType

	tests := map[string]struct {
		params         investigateFailedPlanApplicationParams
		objects        []runtime.Object
		cancelCtx      bool
		expectedNode   string
		expectedFilter bool
		expectedError  string
		expectedDetail string
	}{
		"CAPI machine with a machine plan previews a filter on the plan hash": {
			params:         investigateFailedPlanApplicationParams{Cluster: "test-cluster", MachineName: testPlanNode},
			objects:        []runtime.Object{newPlanMachine(testPlanNode, "test-cluster"), newFullMachinePlanSecret(testPlanNode)},
			expectedNode:   testPlanNodeName,
			expectedFilter: true,
		},
		"CAPI machine without a machine plan previews the recent logs": {
			params:         investigateFailedPlanApplicationParams{Cluster: "test-cluster", MachineName: testPlanNode},
			objects:        []runtime.Object{newPlanMachine(testPlanNode, "test-cluster")},
			expectedNode:   testPlanNodeName,
			expectedDetail: "failed to get machine plan secret",
		},
		"CAPI machine with a machine plan missing the plan previews the recent logs": {
			params: investigateFailedPlanApplicationParams{Cluster: "test-cluster", MachineName: testPlanNode},
			objects: []runtime.Object{
				newPlanMachine(testPlanNode, "test-cluster"),
				newMachinePlanSecret(testPlanNode, map[string][]byte{"plan-state": []byte("failed")}),
			},
			expectedNode:   testPlanNodeName,
			expectedDetail: "no plan found in machine plan resource",
		},
		"log line tail is used when the machine plan is unavailable": {
			params:         investigateFailedPlanApplicationParams{Cluster: "test-cluster", MachineName: testPlanNode, LogLineTail: 123},
			objects:        []runtime.Object{newPlanMachine(testPlanNode, "test-cluster")},
			expectedNode:   testPlanNodeName,
			expectedDetail: "failed to get machine plan secret",
		},
		"CAPI machine without a machine plan and a cancelled context": {
			params:        investigateFailedPlanApplicationParams{Cluster: "test-cluster", MachineName: testPlanNode},
			objects:       []runtime.Object{newPlanMachine(testPlanNode, "test-cluster")},
			cancelCtx:     true,
			expectedError: "context canceled",
		},
		"imported cluster with a machine plan previews a filter on the plan hash": {
			params: investigateFailedPlanApplicationParams{Cluster: "c-plan-imp", MachineName: testImportedMachine, NodeName: testImportedNode},
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-plan-imp", ""),
				newImportedPlanSecret("plan-1", "c-plan-imp", testImportedNode, planSecretType, fullMachinePlanData())),
			expectedNode:   testImportedNode,
			expectedFilter: true,
		},
		"imported cluster without a machine plan previews the recent logs": {
			params:         investigateFailedPlanApplicationParams{Cluster: "c-plan-imp-none", MachineName: testImportedMachine, NodeName: testImportedNode},
			objects:        withObjects(day2OpsEnabled(), newImportedCluster("c-plan-imp-none", "")),
			expectedNode:   testImportedNode,
			expectedDetail: "machine plan not found for node",
		},
		"imported cluster with multiple machine plans previews the recent logs": {
			params: investigateFailedPlanApplicationParams{Cluster: "c-plan-imp-multi", MachineName: testImportedMachine, NodeName: testImportedNode},
			objects: withObjects(day2OpsEnabled(),
				newImportedCluster("c-plan-imp-multi", ""),
				newImportedPlanSecret("plan-1", "c-plan-imp-multi", testImportedNode, planSecretType, fullMachinePlanData()),
				newImportedPlanSecret("plan-2", "c-plan-imp-multi", testImportedNode, planSecretType, fullMachinePlanData())),
			expectedNode:   testImportedNode,
			expectedDetail: "multiple machine plans found",
		},
		"imported cluster without a node name": {
			params:        investigateFailedPlanApplicationParams{Cluster: "c-plan-imp-no-node", MachineName: testImportedMachine},
			objects:       withObjects(day2OpsEnabled(), newImportedCluster("c-plan-imp-no-node", "")),
			expectedError: "Kubernetes node name is required for imported clusters",
		},
		"imported cluster without day two operations": {
			params:        investigateFailedPlanApplicationParams{Cluster: "c-plan-imp-off", MachineName: testImportedMachine, NodeName: testImportedNode},
			objects:       withObjects(day2OpsEnabled(), newImportedCluster("c-plan-imp-off", "false")),
			expectedError: "disabled for imported cluster c-plan-imp-off",
		},
		"hosted cluster": {
			params:        investigateFailedPlanApplicationParams{Cluster: "c-plan-hosted", MachineName: testImportedMachine, NodeName: testImportedNode},
			objects:       withObjects(day2OpsEnabled(), newClusterWithProvider("c-plan-hosted", "aks", nil)),
			expectedError: "cannot be run against hosted clusters",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, dynClient := newImportedTools(tt.objects...)
			ctx := middleware.WithToken(t.Context(), testToken)
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			result, _, err := tools.investigateFailedPlanApplicationPlan(ctx, newToolRequest("investigate-failed-plan-application-plan"), tt.params)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			job := previewedJob(t, result)
			text := result.Content[0].(*mcp.TextContent)
			var planned []struct {
				Detail string `json:"detail"`
			}
			require.NoError(t, json.Unmarshal([]byte(text.Text), &planned))
			require.Len(t, planned, 1)
			if tt.expectedDetail == "" {
				assert.Empty(t, planned[0].Detail)
			} else {
				assert.Contains(t, planned[0].Detail, tt.expectedDetail)
				assert.Contains(t, planned[0].Detail, "without filtering by plan hash")
			}

			podSpec := job.Spec.Template.Spec
			assert.Equal(t, tt.expectedNode, podSpec.NodeName)
			require.Len(t, podSpec.Containers, 1)
			require.Len(t, podSpec.Containers[0].Command, 5)
			script := podSpec.Containers[0].Command[4]

			if tt.expectedFilter {
				// the real hash is not shown, as it can change before the user confirms
				assert.Contains(t, script, "grep PLAN_HASH -A 15 -B 10")
			} else {
				assert.NotContains(t, script, "grep")
				assert.NotContains(t, script, "PLAN_HASH")
				expected := unfilteredScript
				if tt.params.LogLineTail != 0 {
					expected = fmt.Sprintf("echo 'Machine plan unavailable; showing recent system-agent logs'; journalctl -u rancher-system-agent.service --no-pager -o cat | tail -n %d", tt.params.LogLineTail)
				}
				assert.Contains(t, script, expected)
			}

			// the plan is a dry run, so no job should have been created
			_, err = dynClient.Resource(converter.K8sKindsToGVRs["job"]).Namespace(DefaultRancherResourcesNamespace).
				Get(t.Context(), job.Name, metav1.GetOptions{})
			assert.True(t, apierrors.IsNotFound(err), "expected no job to be created, got: %v", err)
		})
	}
}

func TestImportedPlanLookupRejectsInvalidNodeLabel(t *testing.T) {
	tools, _ := newImportedTools()
	_, _, err := getMachinePlanFields(middleware.WithToken(t.Context(), testToken), planTarget{
		imported: true, nodeName: "not a valid label value", planSecretNamespace: "c-invalid-label",
	}, tools.client, zap.NewNop())
	assert.ErrorContains(t, err, "invalid node name for imported machine plan lookup")
}

func TestInvestigateFailedPlanApplicationImportedPlanLookupErrors(t *testing.T) {
	const reason = "Returned recent system-agent logs without filtering by plan hash."

	badTypeSecret := newImportedPlanSecret("plan-1", "c-imp-lookup-type", testImportedNode, machinePlanSecretType, fullMachinePlanData())
	badTypeSecret.Object["type"] = int64(5)

	tests := map[string]struct {
		cluster       string
		nodeName      string
		objects       []runtime.Object
		reactResrc    string
		reactVerb     string
		expectedNote  string
		expectedError string
	}{
		"node name which is not a valid label value": {
			cluster:       "c-imp-lookup-label",
			nodeName:      "not a valid label value",
			objects:       withObjects(day2OpsEnabled(), newImportedCluster("c-imp-lookup-label", ""), newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedError: `Kubernetes node "not a valid label value" does not exist`,
		},
		"listing the machine plans fails": {
			cluster:      "c-imp-lookup-list",
			nodeName:     testImportedNode,
			objects:      withObjects(day2OpsEnabled(), newImportedCluster("c-imp-lookup-list", ""), newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			reactVerb:    "list",
			reactResrc:   "secrets",
			expectedNote: "failed to list imported machine plans for node imported-node-1: rancher is unavailable",
		},
		"secret with a type which is not a string": {
			cluster:      "c-imp-lookup-type",
			nodeName:     testImportedNode,
			objects:      withObjects(day2OpsEnabled(), newImportedCluster("c-imp-lookup-type", ""), badTypeSecret, newLogGathererPod(testImportedMachine, corev1.PodSucceeded)),
			expectedNote: "failed to read type of secret plan-1",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, dynClient := newImportedTools(tt.objects...)
			if tt.reactVerb != "" {
				dynClient.PrependReactor(tt.reactVerb, tt.reactResrc, func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("rancher is unavailable")
				})
			}

			result, _, err := tools.investigateFailedPlanApplication(middleware.WithToken(t.Context(), testToken),
				newToolRequest("investigate-failed-provisioning-plan-application"),
				investigateFailedPlanApplicationParams{Cluster: tt.cluster, MachineName: testImportedMachine, NodeName: tt.nodeName})

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected type *mcp.TextContent")
			assert.Contains(t, text.Text, tt.expectedNote)
			assert.Contains(t, text.Text, reason)
			assert.Contains(t, text.Text, "fake logs")
		})
	}
}

func TestResolveImportedPlanTargetManagementClusterError(t *testing.T) {
	dynClient := newPlanApplicationDynClient(withObjects(day2OpsEnabled(), newImportedCluster("c-resolve-get-error", ""))...)
	tools := newTestTools(dynClient)
	ctx := middleware.WithToken(t.Context(), testToken)

	// resolve the cluster ID up front, which caches it, so that only the lookup of the cluster object fails
	_, err := tools.client.GetClusterID(ctx, testToken, "c-resolve-get-error")
	require.NoError(t, err)
	dynClient.PrependReactor("get", "clusters", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("rancher is unavailable")
	})

	_, err = tools.resolveImportedPlanTarget(ctx, zap.NewNop(), "c-resolve-get-error", testImportedNode)

	assert.ErrorContains(t, err, "failed to get management cluster c-resolve-get-error")
	assert.ErrorContains(t, err, "rancher is unavailable")
}
