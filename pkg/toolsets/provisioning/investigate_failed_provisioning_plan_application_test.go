package provisioning

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher/pkg/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testPlanNode     = "test-cluster-node-1"
	testToolboxImage = "test-toolbox-image"
	testPlan         = `{"instructions":[{"name":"install","image":"rancher/system-agent-installer-rke2:v1.33.1-rke2r1"}]}`
)

// newPlanApplicationDynClient creates a fake dynamic client for the plan application tests.
func newPlanApplicationDynClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	listKinds := provisioningCustomListKinds()
	listKinds[schema.GroupVersionResource{Version: "v1", Resource: "pods"}] = "PodList"
	listKinds[schema.GroupVersionResource{Version: "v1", Resource: "secrets"}] = "SecretList"
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)
	sequence := 0
	dynClient.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		job := action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		if job.GetName() == "" && job.GetGenerateName() != "" {
			sequence++
			job.SetName(job.GetGenerateName() + strconv.Itoa(sequence))
			job.SetUID(types.UID(job.GetName()))
			for _, object := range objects {
				pod, ok := object.(*unstructured.Unstructured)
				if !ok || pod.GetKind() != "Pod" || pod.GetLabels()["batch.kubernetes.io/controller-uid"] != string(job.GetUID()) {
					continue
				}
				if phase, _, _ := unstructured.NestedString(pod.Object, "status", "phase"); phase == string(corev1.PodFailed) {
					job.Object["status"] = map[string]interface{}{
						"conditions": []interface{}{map[string]interface{}{"type": "Failed", "status": "True"}},
					}
				}
			}
		}
		return false, nil, nil
	})
	return dynClient
}

// testPlanNodeName is deliberately different from testPlanNode (the machine name), as it is on custom clusters.
const testPlanNodeName = "test-node-1"

// newPlanMachine creates a running CAPI machine which has registered the node testPlanNodeName.
func newPlanMachine(name, clusterName string) *unstructured.Unstructured {
	machine := newCAPIMachine(name, "fleet-default", clusterName, "Running", "")
	machine.Object["status"].(map[string]interface{})["nodeRef"] = map[string]interface{}{
		"name": testPlanNodeName,
	}
	return machine
}

// newMachinePlanSecret creates a test machine plan secret for the given node
func newMachinePlanSecret(nodeName string, data map[string][]byte) *unstructured.Unstructured {
	secret := &corev1.Secret{
		APIVersion: "v1",
		Kind:       "Secret",
		Name:       fmt.Sprintf("%s-machine-plan", nodeName),
		Namespace:  "fleet-default",
		Type:       "rke.cattle.io/machine-plan",
		Data:       data,
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: unstructuredObj}
}

// newFullMachinePlanSecret creates a test machine plan secret containing a plan and all reported plan fields
func newFullMachinePlanSecret(nodeName string) *unstructured.Unstructured {
	return newMachinePlanSecret(nodeName, fullMachinePlanData())
}

// fullMachinePlanData returns the data of a machine plan secret containing a plan and all reported plan fields
func fullMachinePlanData() map[string][]byte {
	return map[string][]byte{
		"plan":            []byte(testPlan),
		"failure-count":   []byte("3"),
		"last-apply-time": []byte("2026-10-20T12:00:00Z"),
		"max-failures":    []byte("5"),
		"plan-revision":   []byte("12345"),
		"plan-state":      []byte("failed"),
		"probe-statuses":  []byte(`{"kubelet":{"healthy":false}}`),
	}
}

// newLogGathererPod creates a test pod as it would be created by the log gatherer job for the given node.
// The fake dynamic client does not run a job controller, so tests seed this pod up front.
func newLogGathererPod(nodeName string, phase corev1.PodPhase) *unstructured.Unstructured {
	jobName := fmt.Sprintf("%s-systemd-log-gatherer-1", nodeName)
	return newLogGathererPodForJob(jobName, types.UID(jobName), phase)
}

func newLogGathererPodForJob(jobName string, uid types.UID, phase corev1.PodPhase) *unstructured.Unstructured {
	pod := &corev1.Pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       jobName + "-abcde",
		Namespace:  "cattle-system",
		Labels: map[string]string{
			"job-name":                           jobName,
			"batch.kubernetes.io/controller-uid": string(uid),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "journald-reader", Image: testToolboxImage},
			},
		},
		Status: corev1.PodStatus{
			Phase: phase,
		},
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pod)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: unstructuredObj}
}

// newLogGathererJob creates a test job with the same name as the log gatherer job for the given node
func newLogGathererJob(nodeName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "batch/v1",
			"kind":       "Job",
			"metadata": map[string]any{
				"name":      fmt.Sprintf("%s-systemd-log-gatherer", nodeName),
				"namespace": "cattle-system",
			},
		},
	}
}

func TestInvestigateFailedPlanApplication(t *testing.T) {
	fakeToken := "fakeToken"
	expectedChecksum := plan.Checksum([]byte(testPlan))

	expectedSuccessResult := fmt.Sprintf(`{
		"llm": [
			{
				"logs": {
					"journald-reader": "fake logs"
				}
			},
			{
				"checksum": %q,
				"failure-count": "3",
				"last-apply-time": "2026-10-20T12:00:00Z",
				"max-failures": "5",
				"plan-revision": "12345",
				"plan-state": "failed",
				"probe-statuses": "{\"kubelet\":{\"healthy\":false}}"
			}
		]
	}`, expectedChecksum)

	// the logs are returned alone, with a note explaining why they were not filtered by the plan
	expectedFallbackResult := `{
		"llm": {
			"note": "Machine plan unavailable: %s. Returned recent system-agent logs without filtering by plan hash.",
			"resources": [{"logs": {"journald-reader": "fake logs"}}]
		}
	}`

	tests := map[string]struct {
		params        investigateFailedPlanApplicationParams
		fakeClientset kubernetes.Interface
		fakeDynClient *dynamicfake.FakeDynamicClient
		ctxTimeout    time.Duration
		cancelCtx     bool
		verifyJob     bool
		// verifyUnfilteredJob checks the job which is created when there is no plan hash to filter on
		verifyUnfilteredJob bool
		expectedResult      string
		expectedError       string
	}{
		"investigate failed plan with succeeded log gatherer pod": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJob:      true,
			expectedResult: expectedSuccessResult,
		},
		"investigate failed plan with failed log gatherer pod": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodFailed)),
			expectedError: "log-gatherer Pod test-cluster-node-1-systemd-log-gatherer-1-abcde",
		},
		"investigate failed plan with a custom log line tail": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
				LogLineTail: 50,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJob:      true,
			expectedResult: expectedSuccessResult,
		},
		"investigate failed plan with the minimum log line tail": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
				LogLineTail: 30,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJob:      true,
			expectedResult: expectedSuccessResult,
		},
		"investigate failed plan with the maximum log line tail": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
				LogLineTail: 1000,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJob:      true,
			expectedResult: expectedSuccessResult,
		},
		"investigate failed plan with a negative log line tail": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
				LogLineTail: -1,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "logLineTail must be between",
		},
		"investigate failed plan with a log line tail above the maximum": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
				LogLineTail: 1001,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "logLineTail must be between",
		},
		"investigate failed plan on the local cluster": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "local",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "plans are not used to control the local cluster",
		},
		"investigate failed plan with empty node name": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: "  ",
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "machine name is required",
		},
		"investigate failed plan on a cluster without CAPI machines": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "other-cluster", "Running", ""),
				newManagementCluster("test-cluster", true)),
			expectedError: `no CAPI machine resources and its machine provider is ""`,
		},
		"investigate failed plan on an unknown node": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: "unknown-node",
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster")),
			expectedError: "failed to find machine unknown-node",
		},
		"investigate failed plan without a machine plan secret falls back to the logs": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyUnfilteredJob: true,
			expectedResult: fmt.Sprintf(expectedFallbackResult,
				fmt.Sprintf(`failed to get machine plan secret %s-machine-plan in namespace fleet-default: secrets \"%s-machine-plan\" not found`, testPlanNode, testPlanNode)),
		},
		"investigate failed plan with a machine plan secret missing the plan falls back to the logs": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newMachinePlanSecret(testPlanNode, map[string][]byte{"plan-state": []byte("failed")}),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyUnfilteredJob: true,
			expectedResult:      fmt.Sprintf(expectedFallbackResult, "no plan found in machine plan resource"),
		},
		"investigate failed plan without a machine plan secret when the log gatherer job already exists": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newLogGathererJob(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyUnfilteredJob: true,
			expectedResult: fmt.Sprintf(expectedFallbackResult,
				fmt.Sprintf(`failed to get machine plan secret %s-machine-plan in namespace fleet-default: secrets \"%s-machine-plan\" not found`, testPlanNode, testPlanNode)),
		},
		"investigate failed plan without a machine plan secret when the context is cancelled": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true)),
			cancelCtx:     true,
			expectedError: "context canceled",
		},
		"investigate failed plan for a machine with an unreadable node reference": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				func() runtime.Object {
					machine := newPlanMachine(testPlanNode, "test-cluster")
					machine.Object["status"].(map[string]interface{})["nodeRef"] = map[string]interface{}{"name": int64(1)}
					return machine
				}()),
			expectedError: "failed to read the node reference of machine " + testPlanNode,
		},
		"investigate failed plan for a machine which has not registered a node": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Provisioning", "")),
			expectedError: "has not registered a node",
		},
		"investigate failed plan when the log gatherer job already exists": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererJob(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJob:      true,
			expectedResult: expectedSuccessResult,
		},
		"investigate failed plan when the log gatherer pod never completes": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodRunning)),
			// bound the context so the test does not wait for the full pod polling timeout
			ctxTimeout:    1500 * time.Millisecond,
			expectedError: "context deadline exceeded",
		},
		"investigate failed plan on a cluster unknown to rancher": {
			params: investigateFailedPlanApplicationParams{
				// cluster IDs are cached across tests, so use a name no other test registers
				Cluster:     "unregistered-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "unregistered-cluster"),
				newFullMachinePlanSecret(testPlanNode)),
			expectedError: "cluster 'unregistered-cluster' not found",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{
				ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
					return tt.fakeClientset, nil
				},
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return tt.fakeDynClient, nil
				},
			}

			tools := NewTools(test.WrapClient(c, fakeToken), false)
			tools.toolboxImage = testToolboxImage
			req := &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Name: "investigate-failed-provisioning-plan-application",
				},
			}

			ctx := middleware.WithToken(t.Context(), fakeToken)
			if tt.ctxTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.ctxTimeout)
				defer cancel()
			}
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			result, _, err := tools.investigateFailedPlanApplication(ctx, req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.Truef(t, ok, "expected type *mcp.TextContent")
			assert.JSONEq(t, tt.expectedResult, text.Text)

			if tt.verifyJob {
				verifyLogGathererJob(t, tt.fakeDynClient, tt.params.MachineName, testPlanNodeName, expectedChecksum)
			}
			if tt.verifyUnfilteredJob {
				verifyUnfilteredLogGathererJob(t, tt.fakeDynClient, tt.params.MachineName, testPlanNodeName, defaultLogLineTail)
			}
		})
	}
}

// verifyLogGathererJob asserts that the log gatherer job was created on the node and filters logs on the plan checksum
func verifyLogGathererJob(t *testing.T, dynClient *dynamicfake.FakeDynamicClient, machineName, nodeName, expectedChecksum string) {
	t.Helper()

	command := verifyLogGathererJobPodSpec(t, dynClient, machineName, nodeName)
	assert.Contains(t, command, fmt.Sprintf("grep %s", expectedChecksum))
	assert.Contains(t, command, "journalctl -eu rancher-system-agent.service")
}

// verifyUnfilteredLogGathererJob asserts that the log gatherer job was created on the node and returns the tail of
// the logs, as there was no plan to filter them on.
func verifyUnfilteredLogGathererJob(t *testing.T, dynClient *dynamicfake.FakeDynamicClient, machineName, nodeName string, logLineTail int) {
	t.Helper()

	command := verifyLogGathererJobPodSpec(t, dynClient, machineName, nodeName)
	assert.NotContains(t, command, "grep")
	assert.Contains(t, command, "journalctl -eu rancher-system-agent.service --no-pager -o cat | tail -n "+strconv.Itoa(logLineTail))
}

// verifyLogGathererJobPodSpec asserts what is common to all log gatherer jobs and returns the script they run.
func verifyLogGathererJobPodSpec(t *testing.T, dynClient *dynamicfake.FakeDynamicClient, machineName, nodeName string) string {
	t.Helper()

	jobName := fmt.Sprintf("%s-systemd-log-gatherer", machineName)
	var uJob *unstructured.Unstructured
	for _, action := range dynClient.Actions() {
		if action.Matches("create", "jobs") {
			created := action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
			if strings.HasPrefix(created.GetGenerateName(), jobName+"-") {
				uJob = created
			}
		}
		if action.Matches("delete", "jobs") {
			options := action.(k8stesting.DeleteAction).GetDeleteOptions()
			require.NotNil(t, options.PropagationPolicy)
			assert.Equal(t, metav1.DeletePropagationBackground, *options.PropagationPolicy)
		}
	}
	require.NotNil(t, uJob, "expected the log-gatherer Job to have been created")
	assert.Empty(t, uJob.GetName())
	assert.Equal(t, jobName+"-", uJob.GetGenerateName())
	_, err := dynClient.Resource(converter.K8sKindsToGVRs["job"]).Namespace(DefaultRancherResourcesNamespace).
		Get(t.Context(), jobName+"-1", metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err), "expected the log-gatherer Job to have been cleaned up, got %v", err)

	job := &batchv1.Job{}
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(uJob.Object, job))

	podSpec := job.Spec.Template.Spec
	assert.Equal(t, nodeName, podSpec.NodeName)
	assert.Empty(t, podSpec.NodeSelector)
	assert.Nil(t, podSpec.Affinity)
	assert.Equal(t, []corev1.Toleration{{Operator: corev1.TolerationOpExists}}, podSpec.Tolerations)
	require.Len(t, podSpec.Containers, 1)

	container := podSpec.Containers[0]
	assert.Equal(t, "journald-reader", container.Name)
	assert.Equal(t, testToolboxImage, container.Image)
	require.Len(t, container.Command, 5)
	assert.Equal(t, []string{"chroot", "/host", "bash", "-c"}, container.Command[:4])

	return container.Command[4]
}

func TestInvestigateFailedPlanApplicationLogLineTailDefault(t *testing.T) {
	fakeToken := "fakeToken"
	dynClient := newPlanApplicationDynClient(
		newPlanMachine(testPlanNode, "test-cluster"))
	c := &client.Client{
		ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
			return newFakeClientSet(), nil
		},
		DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
			return dynClient, nil
		},
	}
	tools := NewTools(test.WrapClient(c, fakeToken), false)
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "investigate-failed-provisioning-plan-application"}}
	ctx := middleware.WithToken(t.Context(), fakeToken)

	tests := map[string]struct {
		input    int
		expected int
	}{
		"unset uses the default": {input: 0, expected: defaultLogLineTail},
		"provided value is kept": {input: 42, expected: 42},
		"maximum value is kept":  {input: 1000, expected: 1000},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			params := investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
				LogLineTail: tt.input,
			}
			_, err := params.validate(ctx, tools, req)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, params.LogLineTail)
		})
	}
}
