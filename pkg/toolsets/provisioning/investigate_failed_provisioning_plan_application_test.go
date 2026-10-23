package provisioning

import (
	"context"
	"fmt"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)
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
	return newMachinePlanSecret(nodeName, map[string][]byte{
		"plan":            []byte(testPlan),
		"failure-count":   []byte("3"),
		"last-apply-time": []byte("2026-10-20T12:00:00Z"),
		"max-failures":    []byte("5"),
		"plan-revision":   []byte("12345"),
		"plan-state":      []byte("failed"),
		"probe-statuses":  []byte(`{"kubelet":{"healthy":false}}`),
	})
}

// newLogGathererPod creates a test pod as it would be created by the log gatherer job for the given node.
// The fake dynamic client does not run a job controller, so tests seed this pod up front.
func newLogGathererPod(nodeName string, phase corev1.PodPhase) *unstructured.Unstructured {
	jobName := fmt.Sprintf("%s-systemd-log-gatherer", nodeName)
	pod := &corev1.Pod{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       jobName + "-abcde",
		Namespace:  "cattle-system",
		Labels: map[string]string{
			"job-name": jobName,
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

	tests := map[string]struct {
		params         investigateFailedPlanApplicationParams
		fakeClientset  kubernetes.Interface
		fakeDynClient  *dynamicfake.FakeDynamicClient
		ctxTimeout     time.Duration
		verifyJob      bool
		expectedResult string
		expectedError  string
	}{
		"investigate failed plan with succeeded log gatherer pod": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", ""),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodSucceeded)),
			verifyJob:      true,
			expectedResult: expectedSuccessResult,
		},
		"investigate failed plan with failed log gatherer pod": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", ""),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererPod(testPlanNode, corev1.PodFailed)),
			verifyJob:      true,
			expectedResult: expectedSuccessResult,
		},
		"investigate failed plan on the local cluster": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "local",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "plans are not used to control the local cluster",
		},
		"investigate failed plan with empty node name": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: "  ",
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "node name is required",
		},
		"investigate failed plan on a cluster without CAPI machines": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "other-cluster", "Running", "")),
			expectedError: "did not find any CAPI machine resources for cluster test-cluster",
		},
		"investigate failed plan on an unknown node": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: "unknown-node",
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", "")),
			expectedError: "failed to find node unknown-node",
		},
		"investigate failed plan without a machine plan secret": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", "")),
			expectedError: fmt.Sprintf(`secrets "%s-machine-plan" not found`, testPlanNode),
		},
		"investigate failed plan with a machine plan secret missing the plan": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", ""),
				newMachinePlanSecret(testPlanNode, map[string][]byte{"plan-state": []byte("failed")})),
			expectedError: "no plan found in machine plan resource",
		},
		"investigate failed plan when the log gatherer job already exists": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", ""),
				newManagementCluster("test-cluster", true),
				newFullMachinePlanSecret(testPlanNode),
				newLogGathererJob(testPlanNode)),
			expectedError: "already exists",
		},
		"investigate failed plan when the log gatherer pod never completes": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", ""),
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
				Cluster:  "unregistered-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "unregistered-cluster", "Running", ""),
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
				verifyLogGathererJob(t, tt.fakeDynClient, tt.params.NodeName, expectedChecksum)
			}
		})
	}
}

// verifyLogGathererJob asserts that the log gatherer job was created on the node and filters logs on the plan checksum
func verifyLogGathererJob(t *testing.T, dynClient *dynamicfake.FakeDynamicClient, nodeName, expectedChecksum string) {
	t.Helper()

	uJob, err := dynClient.Resource(converter.K8sKindsToGVRs["job"]).Namespace(DefaultRancherResourcesNamespace).
		Get(t.Context(), fmt.Sprintf("%s-systemd-log-gatherer", nodeName), metav1.GetOptions{})
	require.NoError(t, err)

	job := &batchv1.Job{}
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(uJob.Object, job))

	podSpec := job.Spec.Template.Spec
	assert.Equal(t, nodeName, podSpec.NodeSelector["kubernetes.io/hostname"])
	require.Len(t, podSpec.Containers, 1)

	container := podSpec.Containers[0]
	assert.Equal(t, "journald-reader", container.Name)
	assert.Equal(t, testToolboxImage, container.Image)
	require.Len(t, container.Command, 5)
	assert.Equal(t, []string{"chroot", "/host", "bash", "-c"}, container.Command[:4])
	assert.Contains(t, container.Command[4], fmt.Sprintf("grep %s", expectedChecksum))
	assert.Contains(t, container.Command[4], "journalctl -eu rancher-system-agent.service")
}
