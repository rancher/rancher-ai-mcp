package provisioning

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	"go.uber.org/zap"
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
	const (
		olderFailedChecksum     = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
		currentPlanChecksumNote = "The checksum is of the current plan. The system-agent has not recorded a failed plan checksum."
		removedPlanChecksumNote = "The checksum is of the last failed plan, as recorded by the system-agent. The plan has been removed from the machine plan."
	)

	expectedSuccessResult := fmt.Sprintf(`{
		"llm": [
			{
				"logs": {
					"journald-reader": "fake logs"
				}
			},
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
	}`, expectedChecksum, currentPlanChecksumNote)

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
		// verifyJobChecksum checks the job filters the logs on this checksum, instead of the one of the test plan
		verifyJobChecksum string
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
				LogLineTail: 200,
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
				LogLineTail: 100,
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
		"investigate failed plan with a log line tail below the minimum": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
				LogLineTail: 99,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "logLineTail must be between 100 and 1000",
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
		"investigate failed plan with a removed plan uses the failed checksum to filter the logs": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newMachinePlanSecret(testPlanNode, map[string][]byte{
					"plan-state":      []byte("failed"),
					"failed-checksum": []byte(expectedChecksum),
				}),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJob: true,
			expectedResult: fmt.Sprintf(`{
				"llm": [
					{"logs": {"journald-reader": "fake logs"}},
					{
						"checksum": %q,
						"checksum-note": %q,
						"failed-checksum": %q,
						"failure-count": "",
						"last-apply-time": "",
						"max-failures": "",
						"plan-revision": "",
						"plan-state": "failed",
						"probe-statuses": ""
					}
				]
			}`, expectedChecksum, removedPlanChecksumNote, expectedChecksum),
		},
		"investigate failed plan prefers the failed checksum over a newer plan when the plan state is failed": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newMachinePlanSecret(testPlanNode, map[string][]byte{
					"plan":            []byte(testPlan),
					"plan-state":      []byte("failed"),
					"failed-checksum": []byte(olderFailedChecksum),
				}),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJobChecksum: olderFailedChecksum,
			expectedResult: fmt.Sprintf(`{
				"llm": [
					{"logs": {"journald-reader": "fake logs"}},
					{
						"checksum": %q,
						"checksum-note": %q,
						"failed-checksum": %q,
						"failure-count": "",
						"last-apply-time": "",
						"max-failures": "",
						"plan-revision": "",
						"plan-state": "failed",
						"probe-statuses": ""
					}
				]
			}`, olderFailedChecksum, fmt.Sprintf("The checksum is of the last failed plan, as recorded by the system-agent. The plan state is failed, but the current plan has a different checksum (%s), so the failure may be of an earlier plan.", expectedChecksum), olderFailedChecksum),
		},
		"investigate failed plan uses the current plan checksum while a newer plan is in progress": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newMachinePlanSecret(testPlanNode, map[string][]byte{
					"plan":            []byte(testPlan),
					"plan-state":      []byte("in-progress"),
					"failed-checksum": []byte(olderFailedChecksum),
				}),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJobChecksum: expectedChecksum,
			expectedResult: fmt.Sprintf(`{
				"llm": [
					{"logs": {"journald-reader": "fake logs"}},
					{
						"checksum": %q,
						"checksum-note": %q,
						"failed-checksum": %q,
						"failure-count": "",
						"last-apply-time": "",
						"max-failures": "",
						"plan-revision": "",
						"plan-state": "in-progress",
						"probe-statuses": ""
					}
				]
			}`, expectedChecksum, "The checksum is of the current plan, which is not in a failed state. A plan failed before it, but that plan is not shown.", olderFailedChecksum),
		},
		"investigate failed plan uses the current plan checksum when the failed checksum is malformed": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newMachinePlanSecret(testPlanNode, map[string][]byte{
					"plan":            []byte(testPlan),
					"plan-state":      []byte("failed"),
					"failed-checksum": []byte("not-a-checksum"),
				}),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJobChecksum: expectedChecksum,
			expectedResult: fmt.Sprintf(`{
				"llm": [
					{"logs": {"journald-reader": "fake logs"}},
					{
						"checksum": %q,
						"checksum-note": %q,
						"failed-checksum": "",
						"failure-count": "",
						"last-apply-time": "",
						"max-failures": "",
						"plan-revision": "",
						"plan-state": "failed",
						"probe-statuses": ""
					}
				]
			}`, expectedChecksum, "The checksum is of the current plan. The failed-checksum recorded by the system-agent is not a valid checksum, so it was ignored."),
		},
		"investigate failed plan with a malformed failed checksum falls back to the logs": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newManagementCluster("test-cluster", true),
				newMachinePlanSecret(testPlanNode, map[string][]byte{
					"plan-state":      []byte("failed"),
					"failed-checksum": []byte("x; touch /tmp/pwned #"),
				}),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyUnfilteredJob: true,
			expectedResult:      fmt.Sprintf(expectedFallbackResult, "no plan found in machine plan resource, and its failed-checksum is not a valid checksum"),
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
			if tt.verifyJobChecksum != "" {
				verifyLogGathererJob(t, tt.fakeDynClient, tt.params.MachineName, testPlanNodeName, tt.verifyJobChecksum)
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
	// the execution lines are emitted after the context lines so they survive the tail of the pod logs
	applyinatorFilter := fmt.Sprintf("Applying one-time instructions for plan with checksum %s/ {split(\"\", l); n=0; seen=1}", expectedChecksum)
	require.Contains(t, command, applyinatorFilter)
	// the output of the plan's instructions is prefixed with the checksum and the index of the instruction
	require.Contains(t, command, fmt.Sprintf("grep -F -e '[Applyinator]' -e 'error executing instruction' -e '[%s_' | awk -v head=", expectedChecksum))
	assert.Greater(t, strings.Index(command, applyinatorFilter), strings.Index(command, fmt.Sprintf("grep %s -A 15 -B 10", expectedChecksum)))
	assert.Contains(t, command, "-A 15 -B 10 | tail -n ")
	assert.Contains(t, command, "journalctl -u rancher-system-agent.service")
}

// verifyUnfilteredLogGathererJob asserts that the log gatherer job was created on the node and returns the tail of
// the logs, as there was no plan to filter them on.
func verifyUnfilteredLogGathererJob(t *testing.T, dynClient *dynamicfake.FakeDynamicClient, machineName, nodeName string, logLineTail int) {
	t.Helper()

	command := verifyLogGathererJobPodSpec(t, dynClient, machineName, nodeName)
	assert.NotContains(t, command, "grep")
	assert.Contains(t, command, "journalctl -u rancher-system-agent.service --no-pager -o cat | tail -n "+strconv.Itoa(logLineTail))
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
		"provided value is kept": {input: 123, expected: 123},
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

const (
	logsTestChecksum = "10405428a216903b96cce03e6c7c5ea48af1e5cb9d2fe348d14579a69fb9476b"
	logsTestPrefix   = `Nov 25 15:37:01 node rancher-system-agent[16606]: time="2026-11-25T15:37:01Z" level=`
	executionHeader  = "--- system-agent execution lines for the machine plan hash ---"
)

// logScriptOptions describes the node a log gathering script is run against.
type logScriptOptions struct {
	logLineTail int
	journal     []string
	// statusLines is the number of lines printed by the stand-in for systemctl status
	statusLines int
	// statusExitCode is the exit code of the stand-in for systemctl status
	statusExitCode int
}

func requireBash(t *testing.T) string {
	t.Helper()

	bash, err := exec.LookPath("bash")
	require.NoError(t, err, "bash is required to run the log gathering script")
	return bash
}

// runLogScript builds the log gathering script for the checksum and runs it against the journal. It returns the output.
func runLogScript(t *testing.T, opts logScriptOptions) string {
	t.Helper()

	journalFile := filepath.Join(t.TempDir(), "journal.log")
	require.NoError(t, os.WriteFile(journalFile, []byte(strings.Join(opts.journal, "\n")+"\n"), 0o600))

	params := investigateFailedPlanApplicationParams{
		MachineName: testPlanNode,
		LogLineTail: opts.logLineTail,
		target:      planTarget{nodeName: testPlanNode},
	}
	uJob, err := buildFailedPlanLogGathererJob(params, testToolboxImage, logsTestChecksum, zap.NewNop())
	require.NoError(t, err)
	job := &batchv1.Job{}
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(uJob.Object, job))
	require.Len(t, job.Spec.Template.Spec.Containers, 1)
	command := job.Spec.Template.Spec.Containers[0].Command
	require.Equal(t, []string{"chroot", "/host", "bash", "-c"}, command[:4])
	script := command[len(command)-1]

	// the script runs in the node's root, where the journal and systemctl exist. Stand in for them.
	systemctl := fmt.Sprintf("%sreturn %d", strings.Repeat("echo 'status line'; ", opts.statusLines), opts.statusExitCode)
	// -e implies -n1000, which hides everything but the last 1000 entries of the journal
	journalctl := fmt.Sprintf(`case "$*" in "-u rancher-system-agent.service --no-pager -o cat") cat %[1]s;; "-eu rancher-system-agent.service --no-pager -o cat") tail -n 1000 %[1]s;; *) echo "unexpected journalctl arguments: $*" >&2; return 1;; esac`, journalFile)
	stubs := fmt.Sprintf("journalctl() { %s; }; systemctl() { %s; }; ", journalctl, systemctl)
	out, err := exec.Command(requireBash(t), "-c", stubs+script).CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

func applyinatorLine(msg string) string {
	return logsTestPrefix + `info msg="[Applyinator] ` + msg + `"`
}

func applyingLine() string {
	return applyinatorLine("Applying one-time instructions for plan with checksum " + logsTestChecksum)
}

func repeatedFailureLine() string {
	return logsTestPrefix + `error msg="[K8s] Maximum failure threshold exceeded for plan with checksum value of ` + logsTestChecksum + `, (failures: 1, threshold: 1)"`
}

// TestFailedPlanLogCommandKeepsTheExecution runs the generated log gathering script against a journal in which a failed
// plan logs its checksum many times after the execution. The execution lines must be emitted last, including the ones
// which do not mention the checksum, and nothing from before the plan may be included.
func TestFailedPlanLogCommandKeepsTheExecution(t *testing.T) {
	requireBash(t)

	execution := []string{
		applyingLine(),
		applyinatorLine("No image provided, creating empty working directory /var/lib/rancher/agent/work/20261125-153701/" + logsTestChecksum + "_0"),
		applyinatorLine("Running command: /bin/sh [-c grep -rE -q token /etc/rancher/rke2/config.yaml || (exit 1)]"),
		// the output of an instruction does not mention [Applyinator]
		logsTestPrefix + `info msg="[` + logsTestChecksum + `_0:stderr]: unable to join: connection refused"`,
		applyinatorLine("Command /bin/sh [-c grep -rE -q token /etc/rancher/rke2/config.yaml || (exit 1)] finished with err: <nil> and exit code: 1"),
		logsTestPrefix + `error msg="error executing instruction 0: <nil>"`,
	}
	earlier := applyinatorLine("Applying one-time instructions for plan with checksum 0000000000000000000000000000000000000000000000000000000000000000")

	journal := []string{earlier, logsTestPrefix + `info msg="[K8s] unrelated"`}
	journal = append(journal, execution...)
	journal = append(journal, logsTestPrefix+`info msg="[K8s] updated plan secret c-vbh59/imported-machine-plan with feedback"`)
	for range 50 {
		journal = append(journal, repeatedFailureLine())
	}

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})

	// the execution lines follow the separator, after the context lines around the checksum
	_, executionSection, found := strings.Cut(out, executionHeader+"\n")
	require.True(t, found, out)
	assert.Equal(t, strings.Join(execution, "\n"), strings.TrimSpace(executionSection))

	// the output is tailed by the pod log retrieval, so only the final lines are guaranteed
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.Equal(t, execution, lines[len(lines)-len(execution):])
}

// TestFailedPlanLogCommandShowsTheLatestExecution checks that when a plan is applied several times, only the latest
// application is shown as the execution.
func TestFailedPlanLogCommandShowsTheLatestExecution(t *testing.T) {
	requireBash(t)

	firstAttempt := []string{
		applyingLine(),
		applyinatorLine("Running command: first attempt"),
		logsTestPrefix + `error msg="error executing instruction 0: first attempt"`,
	}
	latestAttempt := []string{
		applyingLine(),
		applyinatorLine("Running command: latest attempt"),
		logsTestPrefix + `error msg="error executing instruction 0: latest attempt"`,
	}

	var journal []string
	journal = append(journal, firstAttempt...)
	journal = append(journal, repeatedFailureLine())
	journal = append(journal, latestAttempt...)
	journal = append(journal, repeatedFailureLine(), repeatedFailureLine())

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})

	_, executionSection, found := strings.Cut(out, executionHeader+"\n")
	require.True(t, found, out)
	assert.Equal(t, strings.Join(latestAttempt, "\n"), strings.TrimSpace(executionSection))
}

// TestFailedPlanLogCommandWithoutTheApplication checks that when the line announcing the application has been rotated
// out of the journal, the execution starts at the first mention of the checksum instead.
func TestFailedPlanLogCommandWithoutTheApplication(t *testing.T) {
	requireBash(t)

	first := applyinatorLine("Running command: mentions " + logsTestChecksum)
	journal := []string{
		applyinatorLine("Running command: before the checksum"),
		first,
		applyinatorLine("Running command: after the checksum"),
	}

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})

	_, executionSection, found := strings.Cut(out, executionHeader+"\n")
	require.True(t, found, out)
	assert.Equal(t, strings.Join(journal[1:], "\n"), strings.TrimSpace(executionSection))
}

// TestFailedPlanLogCommandWithAVeryLongJournal checks that the execution is still found when the journal is much larger
// than the number of lines kept in memory.
func TestFailedPlanLogCommandWithAVeryLongJournal(t *testing.T) {
	requireBash(t)

	latestAttempt := []string{
		applyingLine(),
		applyinatorLine("Running command: latest attempt"),
		logsTestPrefix + `error msg="error executing instruction 0: latest attempt"`,
	}

	var journal []string
	journal = append(journal, applyingLine(), applyinatorLine("Running command: first attempt"))
	for range 3 * maxExecutionScanLines {
		journal = append(journal, applyinatorLine("Running periodic instruction"))
	}
	journal = append(journal, latestAttempt...)

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})

	_, executionSection, found := strings.Cut(out, executionHeader+"\n")
	require.True(t, found, out)
	assert.Equal(t, strings.Join(latestAttempt, "\n"), strings.TrimSpace(executionSection))
}

// TestFailedPlanLogCommandWithoutTheApplicationInALongJournal checks that when there is no application line to start
// from, only the lines kept in memory after the first mention of the checksum are considered.
func TestFailedPlanLogCommandWithoutTheApplicationInALongJournal(t *testing.T) {
	requireBash(t)

	var journal []string
	for i := range 3 * maxExecutionScanLines {
		journal = append(journal, applyinatorLine(fmt.Sprintf("Running command: step %d mentions %s", i, logsTestChecksum)))
	}

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})

	_, executionSection, found := strings.Cut(out, executionHeader+"\n")
	require.True(t, found, out)
	lines := strings.Split(strings.TrimSpace(executionSection), "\n")
	require.Len(t, lines, defaultLogLineTail/2)
	assert.Contains(t, lines[0], "step 0 ")

	// the first mention is the first line kept, so the last line kept is the one at the cap
	journal = []string{repeatedFailureLine()}
	// the cap is spelled out here so that changing it is a deliberate choice
	for i := 2; i < 5000; i++ {
		journal = append(journal, fmt.Sprintf("unrelated line %d", i))
	}
	journal = append(journal,
		applyinatorLine("Running command: last line kept"),
		applyinatorLine("Running command: first line dropped"))

	out = runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})
	assert.Contains(t, out, "last line kept")
	assert.NotContains(t, out, "first line dropped")
}

// TestFailedPlanLogCommandFitsTheMinimumLogLineTail checks that at the smallest allowed tail, the last mention of the
// checksum and the failure of the execution survive when the pod logs are tailed, even with a full size execution and a
// long service status.
func TestFailedPlanLogCommandFitsTheMinimumLogLineTail(t *testing.T) {
	requireBash(t)

	// the execution is capped at half the tail
	failure := logsTestPrefix + `error msg="error executing instruction 0: <nil>"`
	execution := []string{applyingLine()}
	for i := 1; i < 2*minLogLineTail; i++ {
		execution = append(execution, applyinatorLine(fmt.Sprintf("Running command: step %d", i)))
	}
	execution = append(execution, failure)
	lastMention := logsTestPrefix + `error msg="last mention of ` + logsTestChecksum + `"`

	var journal []string
	for i := range 100 {
		journal = append(journal, fmt.Sprintf("unrelated line %d", i))
	}
	journal = append(journal, execution...)
	// periodic instructions keep logging after the execution, which must not push the execution out of the tail
	for range 80 {
		journal = append(journal, applyinatorLine("Running periodic instruction"))
	}
	for range 100 {
		journal = append(journal, repeatedFailureLine())
	}
	journal = append(journal, lastMention)
	for i := range 20 {
		journal = append(journal, fmt.Sprintf("trailing line %d", i))
	}

	const statusLines = 12
	out := runLogScript(t, logScriptOptions{logLineTail: minLogLineTail, journal: journal, statusLines: statusLines})

	// the context, separator, execution and status make up the output, which the pod then tails
	const contextLines, executionLines = 40, minLogLineTail / 2
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.Len(t, lines, contextLines+1+executionLines+statusLines)
	podLogs := strings.Join(lines[len(lines)-minLogLineTail:], "\n")

	assert.Contains(t, podLogs, lastMention)
	assert.Contains(t, podLogs, failure)
	assert.Contains(t, podLogs, "Running command: step 1\"")
	assert.NotContains(t, podLogs, "Running periodic instruction")
}

// TestFailedPlanLogCommandWithMoreThanAThousandEntries checks that the execution is found when it is older than the last
// 1000 entries of the journal.
func TestFailedPlanLogCommandWithMoreThanAThousandEntries(t *testing.T) {
	requireBash(t)

	execution := []string{
		applyingLine(),
		applyinatorLine("Running command: failing"),
		logsTestPrefix + `error msg="error executing instruction 0: <nil>"`,
	}
	journal := append([]string{}, execution...)
	for range 2000 {
		journal = append(journal, repeatedFailureLine())
	}

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})

	_, executionSection, found := strings.Cut(out, executionHeader+"\n")
	require.True(t, found, out)
	assert.Equal(t, strings.Join(execution, "\n"), strings.TrimSpace(executionSection))
}

// TestFailedPlanLogCommandWithAShortExecution checks that when an execution fails within the lines kept from its start,
// the periodic logs which follow the failure are not shown with it.
func TestFailedPlanLogCommandWithAShortExecution(t *testing.T) {
	requireBash(t)

	execution := []string{
		applyingLine(),
		applyinatorLine("Running command: failing"),
		logsTestPrefix + `error msg="error executing instruction 0: <nil>"`,
	}
	journal := append([]string{}, execution...)
	for range 10 {
		journal = append(journal, applyinatorLine("Running periodic instruction"))
	}
	journal = append(journal, repeatedFailureLine())

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})

	_, executionSection, found := strings.Cut(out, executionHeader+"\n")
	require.True(t, found, out)
	assert.Equal(t, strings.Join(execution, "\n"), strings.TrimSpace(executionSection))
}

// TestFailedPlanLogCommandWithAVerboseExecution checks that when an instruction is very verbose, the lines leading up to
// its failure are kept with the start of the execution, and are not displaced by the periodic logs which follow.
func TestFailedPlanLogCommandWithAVerboseExecution(t *testing.T) {
	requireBash(t)

	failure := logsTestPrefix + `error msg="error executing instruction 0: <nil>"`
	cause := logsTestPrefix + `info msg="[` + logsTestChecksum + `_0:stderr]: fatal: unable to join the cluster"`
	execution := []string{applyingLine()}
	for i := range 300 {
		execution = append(execution, logsTestPrefix+fmt.Sprintf(`info msg="[%s_0:stdout]: output line %d"`, logsTestChecksum, i))
	}
	execution = append(execution, cause, failure)

	journal := append([]string{}, execution...)
	for range 100 {
		journal = append(journal, applyinatorLine("Running periodic instruction"), repeatedFailureLine())
	}

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal})

	_, executionSection, found := strings.Cut(out, executionHeader+"\n")
	require.True(t, found, out)
	lines := strings.Split(strings.TrimSpace(executionSection), "\n")

	// half of the budget is the start of the execution
	const headLines = defaultLogLineTail / 4
	assert.Equal(t, execution[:headLines], lines[:headLines])
	assert.Contains(t, lines[headLines], "execution lines omitted")

	// the rest is what led to the failure, which is the last thing shown
	assert.Equal(t, execution[len(execution)-(len(lines)-headLines-1):], lines[headLines+1:])
	assert.Contains(t, lines, cause)
	assert.Equal(t, failure, lines[len(lines)-1])
	assert.LessOrEqual(t, len(lines), defaultLogLineTail/2)
	assert.NotContains(t, executionSection, "Running periodic instruction")
}

// TestFailedPlanLogCommandWithoutMatchingLogs checks that the tail of the service logs is returned when the checksum is
// not in the journal, and that a failing service status is reported.
func TestFailedPlanLogCommandWithoutMatchingLogs(t *testing.T) {
	requireBash(t)

	var journal []string
	for i := range 40 {
		journal = append(journal, fmt.Sprintf("unrelated line %d", i))
	}

	out := runLogScript(t, logScriptOptions{logLineTail: defaultLogLineTail, journal: journal, statusLines: 2, statusExitCode: 3})

	assert.Contains(t, out, "No logs found for the machine plan hash")
	assert.NotContains(t, out, executionHeader)
	assert.NotContains(t, out, "unrelated line 14\n")
	assert.Contains(t, out, "unrelated line 15\n")
	assert.Contains(t, out, "unrelated line 39\n")
	assert.Contains(t, out, "systemctl status returned exit code 3")
}

func TestBuildFailedPlanLogGathererJobRejectsUnsafeHash(t *testing.T) {
	params := investigateFailedPlanApplicationParams{
		MachineName: testPlanNode,
		LogLineTail: defaultLogLineTail,
		target:      planTarget{nodeName: testPlanNode},
	}
	for _, hash := range []string{"x; touch /tmp/pwned #", "a'b", "-v", "a\nb", "a/b"} {
		_, err := buildFailedPlanLogGathererJob(params, testToolboxImage, hash, zap.NewNop())
		assert.ErrorContains(t, err, "invalid plan hash", hash)
	}
}

// TestGetMachinePlanFieldsChecksum checks which checksum is reported for each state of a machine plan.
func TestGetMachinePlanFieldsChecksum(t *testing.T) {
	currentChecksum := plan.Checksum([]byte(testPlan))
	failedChecksum := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

	const (
		currentNote = "The checksum is of the current plan. The system-agent has not recorded a failed plan checksum."
		failedNote  = "The checksum is of the last failed plan, as recorded by the system-agent."
	)
	notFailedNote := "The checksum is of the current plan, which is not in a failed state. A plan failed before it, but that plan is not shown."

	tests := map[string]struct {
		data             map[string][]byte
		expectedChecksum string
		expectedNote     string
		expectedError    string
		// expectedFailedChecksum is the recorded failure which is reported, whether it was used or not
		expectedFailedChecksum string
	}{
		"failed plan without a recorded failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("failed")},
			expectedChecksum:       currentChecksum,
			expectedNote:           currentNote,
			expectedFailedChecksum: "",
		},
		"failed plan which matches the recorded failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("failed"), "failed-checksum": []byte(currentChecksum)},
			expectedChecksum:       currentChecksum,
			expectedNote:           failedNote + " It is also the current plan.",
			expectedFailedChecksum: currentChecksum,
		},
		"failed state with a different recorded failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("failed"), "failed-checksum": []byte(failedChecksum)},
			expectedChecksum:       failedChecksum,
			expectedNote:           fmt.Sprintf("%s The plan state is failed, but the current plan has a different checksum (%s), so the failure may be of an earlier plan.", failedNote, currentChecksum),
			expectedFailedChecksum: failedChecksum,
		},
		"empty state with a different recorded failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "failed-checksum": []byte(failedChecksum)},
			expectedChecksum:       failedChecksum,
			expectedNote:           fmt.Sprintf("%s The current plan has a different checksum (%s), so it has not been applied yet or has not failed since.", failedNote, currentChecksum),
			expectedFailedChecksum: failedChecksum,
		},
		"pending plan after a failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("pending"), "failed-checksum": []byte(failedChecksum)},
			expectedChecksum:       currentChecksum,
			expectedNote:           notFailedNote,
			expectedFailedChecksum: failedChecksum,
		},
		"in progress plan after a failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("in-progress"), "failed-checksum": []byte(failedChecksum)},
			expectedChecksum:       currentChecksum,
			expectedNote:           notFailedNote,
			expectedFailedChecksum: failedChecksum,
		},
		"cancelled plan after a failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("cancelled"), "failed-checksum": []byte(failedChecksum)},
			expectedChecksum:       currentChecksum,
			expectedNote:           notFailedNote,
			expectedFailedChecksum: failedChecksum,
		},
		"succeeded plan with a stale recorded failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("succeeded"), "failed-checksum": []byte(failedChecksum)},
			expectedChecksum:       currentChecksum,
			expectedNote:           notFailedNote,
			expectedFailedChecksum: failedChecksum,
		},
		"in progress plan which failed before": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("in-progress"), "failed-checksum": []byte(currentChecksum)},
			expectedChecksum:       currentChecksum,
			expectedNote:           "The checksum is of the current plan, which failed before and is not in a failed state now.",
			expectedFailedChecksum: currentChecksum,
		},
		"succeeded plan without a recorded failure": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("succeeded"), "failed-checksum": []byte{}},
			expectedChecksum:       currentChecksum,
			expectedNote:           currentNote,
			expectedFailedChecksum: "",
		},
		"removed plan in a failed state": {
			data:                   map[string][]byte{"plan-state": []byte("failed"), "failed-checksum": []byte(failedChecksum)},
			expectedChecksum:       failedChecksum,
			expectedNote:           failedNote + " The plan has been removed from the machine plan.",
			expectedFailedChecksum: failedChecksum,
		},
		"removed plan in a state which is not failed": {
			data:                   map[string][]byte{"plan-state": []byte("pending"), "failed-checksum": []byte(failedChecksum)},
			expectedChecksum:       failedChecksum,
			expectedNote:           failedNote + " The plan has been removed from the machine plan.",
			expectedFailedChecksum: failedChecksum,
		},
		"malformed recorded failure with a plan": {
			data:                   map[string][]byte{"plan": []byte(testPlan), "plan-state": []byte("failed"), "failed-checksum": []byte("x; touch /tmp/pwned #")},
			expectedChecksum:       currentChecksum,
			expectedNote:           "The checksum is of the current plan. The failed-checksum recorded by the system-agent is not a valid checksum, so it was ignored.",
			expectedFailedChecksum: "",
		},
		"malformed recorded failure without a plan": {
			data:          map[string][]byte{"plan-state": []byte("failed"), "failed-checksum": []byte("x; touch /tmp/pwned #")},
			expectedError: "its failed-checksum is not a valid checksum",
		},
		"neither a plan nor a recorded failure": {
			data:          map[string][]byte{"plan-state": []byte("failed")},
			expectedError: "no plan found in machine plan resource",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := newTestTools(newPlanApplicationDynClient(newMachinePlanSecret(testPlanNode, tt.data)))
			target := planTarget{
				nodeName:            testPlanNode,
				planSecretNamespace: DefaultClusterResourcesNamespace,
				planSecretName:      fmt.Sprintf("%s-machine-plan", testPlanNode),
			}

			fields, checksum, err := getMachinePlanFields(middleware.WithToken(t.Context(), testToken), target, tools.client, zap.NewNop())
			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedChecksum, checksum)
			assert.Equal(t, tt.expectedChecksum, fields["checksum"])
			assert.Equal(t, tt.expectedNote, fields["checksum-note"])
			assert.Equal(t, tt.expectedFailedChecksum, fields["failed-checksum"])
		})
	}
}

// TestValidateResolvesTheCluster checks that the cluster of the tool is resolved before it is used, whichever way it is
// named.
func TestValidateResolvesTheCluster(t *testing.T) {
	validate := func(tools *Tools, params investigateFailedPlanApplicationParams) (investigateFailedPlanApplicationParams, error) {
		_, err := params.validate(middleware.WithToken(t.Context(), testToken), tools, newToolRequest("investigate"))
		return params, err
	}

	t.Run("imported cluster by its display name", func(t *testing.T) {
		cluster := newImportedCluster("c-validate-display", "")
		require.NoError(t, unstructured.SetNestedField(cluster.Object, "Validate Display Name", "spec", "displayName"))
		tools, _ := newImportedTools(withObjects(day2OpsEnabled(), cluster)...)

		params, err := validate(tools, investigateFailedPlanApplicationParams{Cluster: "Validate Display Name", MachineName: testImportedMachine, NodeName: testImportedNode})
		require.NoError(t, err)
		assert.Equal(t, "c-validate-display", params.Cluster)
		assert.Equal(t, planTarget{nodeName: testImportedNode, planSecretNamespace: "c-validate-display", imported: true}, params.target)
	})

	t.Run("cluster which has no management cluster yet", func(t *testing.T) {
		tools := newTestTools(newPlanApplicationDynClient(newProvisioningCluster("test-cluster", "fleet-default", "")))

		_, err := validate(tools, investigateFailedPlanApplicationParams{Cluster: "test-cluster", MachineName: testPlanNode})
		require.ErrorContains(t, err, "cluster test-cluster has not been assigned a management cluster yet")
	})

	t.Run("display name of the local cluster", func(t *testing.T) {
		cluster := newManagementCluster("local", true)
		require.NoError(t, unstructured.SetNestedField(cluster.Object, "my-local", "spec", "displayName"))
		tools := newTestTools(newPlanApplicationDynClient(cluster))

		_, err := validate(tools, investigateFailedPlanApplicationParams{Cluster: "my-local", MachineName: testPlanNode})
		require.ErrorContains(t, err, "plans are not used to control the local cluster")
	})
}
