package provisioning

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	nodeUtils "github.com/rancher/rancher-ai-mcp/pkg/toolsets/provisioning/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func TestInvestigatePlanValidationErrors(t *testing.T) {
	t.Run("CAPI lookup failure is not classified as unsupported", func(t *testing.T) {
		dynClient := newPlanApplicationDynClient(newImportedCluster("c-error-capi", ""))
		forbidden := apierrors.NewForbidden(schema.GroupResource{Group: converter.CAPIGroup, Resource: "machines"}, "", errors.New("list denied"))
		dynClient.PrependReactor("list", "machines", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, forbidden
		})
		tools := newTestTools(dynClient)
		params := investigateFailedPlanApplicationParams{Cluster: "c-error-capi", MachineName: testPlanNode}
		_, err := params.validate(middleware.WithToken(t.Context(), testToken), tools, newToolRequest("investigate"))
		require.ErrorIs(t, err, forbidden)
		assert.ErrorContains(t, err, "failed to list CAPI machines for cluster c-error-capi")
		assert.NotContains(t, err.Error(), "not managed")
	})

	t.Run("missing imported node", func(t *testing.T) {
		tools, dynClient := newImportedTools(withObjects(day2OpsEnabled(), newImportedCluster("c-error-node-missing", ""))...)
		params := investigateFailedPlanApplicationParams{Cluster: "c-error-node-missing", MachineName: testImportedMachine, NodeName: "missing-node"}
		_, err := params.validate(middleware.WithToken(t.Context(), testToken), tools, newToolRequest("investigate"))
		require.ErrorContains(t, err, `Kubernetes node "missing-node" does not exist in cluster c-error-node-missing`)
		for _, action := range dynClient.Actions() {
			assert.NotEqual(t, "create", action.GetVerb())
		}
	})

	t.Run("imported node lookup forbidden", func(t *testing.T) {
		tools, dynClient := newImportedTools(withObjects(day2OpsEnabled(), newImportedCluster("c-error-node-forbidden", ""))...)
		forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, testImportedNode, errors.New("node access denied"))
		dynClient.PrependReactor("get", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, forbidden
		})
		params := investigateFailedPlanApplicationParams{Cluster: "c-error-node-forbidden", MachineName: testImportedMachine, NodeName: testImportedNode}
		_, err := params.validate(middleware.WithToken(t.Context(), testToken), tools, newToolRequest("investigate"))
		require.ErrorIs(t, err, forbidden)
		assert.ErrorContains(t, err, "failed to verify Kubernetes node")
		assert.NotContains(t, err.Error(), "does not exist")
	})
}

func TestInvestigatePlanPreservesBothErrors(t *testing.T) {
	const cluster = "c-error-plan-and-job"
	dynClient := newPlanApplicationDynClient(newPlanMachine(testPlanNode, cluster), newManagementCluster(cluster, true))
	planErr := errors.New("plan access denied")
	jobErr := errors.New("job creation denied")
	dynClient.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, planErr
	})
	dynClient.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, jobErr
	})
	tools := newTestTools(dynClient)
	tools.toolboxImage = testToolboxImage
	_, _, err := tools.investigateFailedPlanApplication(middleware.WithToken(t.Context(), testToken), newToolRequest("investigate"),
		investigateFailedPlanApplicationParams{Cluster: cluster, MachineName: testPlanNode})
	require.ErrorIs(t, err, planErr)
	require.ErrorIs(t, err, jobErr)
	assert.ErrorContains(t, err, "machine plan unavailable")
	assert.ErrorContains(t, err, "failed to create log-gatherer Job")
	assert.ErrorContains(t, err, cluster)
	assert.ErrorContains(t, err, testPlanNodeName)
}

func TestLogGathererFailureDiagnostics(t *testing.T) {
	const jobName = "reader-systemd-log-gatherer"
	const namespace = "diagnostic-test"
	job := newLogGathererJob("reader")
	job.SetNamespace(namespace)
	job.SetUID("reader-job-uid")
	job.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{map[string]interface{}{"type": "Failed", "status": "True", "reason": "FailedCreate", "message": "pod creation denied"}},
	}
	pod := newLogGathererPod("reader", corev1.PodPending)
	pod.SetNamespace(namespace)
	podLabels := pod.GetLabels()
	podLabels["batch.kubernetes.io/controller-uid"] = string(job.GetUID())
	pod.SetLabels(podLabels)
	pod.Object["status"] = map[string]interface{}{
		"phase": "Pending",
		"containerStatuses": []interface{}{map[string]interface{}{
			"name": "journald-reader",
			"state": map[string]interface{}{"waiting": map[string]interface{}{
				"reason": "ImagePullBackOff", "message": "registry denied",
			}},
		}},
	}
	dynClient := newPlanApplicationDynClient(job, pod)
	clientset := fake.NewClientset()
	clientset.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		assert.Equal(t, namespace, action.GetNamespace())
		selector := action.(k8stesting.ListAction).GetListRestrictions().Fields.String()
		if selector == "involvedObject.name="+jobName {
			return true, &corev1.EventList{Items: []corev1.Event{{Reason: "FailedCreate", Message: "quota exceeded"}}}, nil
		}
		return true, &corev1.EventList{}, nil
	})
	details := nodeUtils.JobDiagnostics(t.Context(),
		dynClient.Resource(converter.K8sKindsToGVRs["job"]).Namespace(namespace),
		dynClient.Resource(converter.K8sKindsToGVRs["pod"]).Namespace(namespace),
		clientset, job)
	for _, part := range []string{"Job condition Failed=True: FailedCreate", "pod creation denied", "reader-systemd-log-gatherer-1-abcde", "ImagePullBackOff", "registry denied", "FailedCreate", "quota exceeded"} {
		assert.Contains(t, details, part)
	}
}

func TestLogGathererNoPodAndDiagnosticAccessErrors(t *testing.T) {
	job := newLogGathererJob("reader")
	dynClient := newPlanApplicationDynClient(job)
	clientset := fake.NewClientset()
	clientset.PrependReactor("list", "events", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("events forbidden")
	})
	details := nodeUtils.JobDiagnostics(t.Context(),
		dynClient.Resource(converter.K8sKindsToGVRs["job"]).Namespace(DefaultRancherResourcesNamespace),
		dynClient.Resource(converter.K8sKindsToGVRs["pod"]).Namespace(DefaultRancherResourcesNamespace),
		clientset, job)
	assert.Contains(t, details, "no Pod was created")
	assert.Contains(t, details, "could not read events")
	assert.Contains(t, details, "events forbidden")
}

func TestLogGathererTimedOutPod(t *testing.T) {
	const cluster = "c-error-pending"
	pod := newLogGathererPod(testPlanNode, corev1.PodPending)
	pod.Object["status"] = map[string]interface{}{
		"phase": "Pending",
		"containerStatuses": []interface{}{map[string]interface{}{
			"name": "journald-reader",
			"state": map[string]interface{}{"waiting": map[string]interface{}{
				"reason": "ErrImagePull", "message": "image unavailable",
			}},
		}},
	}
	dynClient := newPlanApplicationDynClient(newManagementCluster(cluster, true), pod)
	tools := newTestTools(dynClient)
	params := investigateFailedPlanApplicationParams{
		Cluster: cluster, MachineName: testPlanNode, LogLineTail: 100,
		target: planTarget{nodeName: testPlanNodeName},
	}
	ctx, cancel := context.WithTimeout(middleware.WithToken(t.Context(), testToken), 50*time.Millisecond)
	defer cancel()
	_, err := createJobAndPollPod(ctx, params, tools.client, testToolboxImage, "", zap.NewNop())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	for _, part := range []string{fmt.Sprintf("%s-systemd-log-gatherer", testPlanNode), DefaultRancherResourcesNamespace, cluster, testPlanNodeName, "ErrImagePull", "image unavailable"} {
		assert.ErrorContains(t, err, part)
	}
}

func TestLogGathererIgnoresPodsFromPreviousRuns(t *testing.T) {
	const cluster = "c-error-stale-pod"
	jobPrefix := testPlanNode + "-systemd-log-gatherer-"
	stalePod := newLogGathererPodForJob(jobPrefix+"old", "old-job-uid", corev1.PodSucceeded)
	currentPod := newLogGathererPodForJob(jobPrefix+"1", types.UID(jobPrefix+"1"), corev1.PodSucceeded)
	priorJob := newLogGathererJob(testPlanNode)
	priorJob.SetName(jobPrefix + "old")
	dynClient := newPlanApplicationDynClient(newManagementCluster(cluster, true), newLogGathererJob(testPlanNode), priorJob, stalePod, currentPod)
	tools := newTestTools(dynClient)
	var loggedPodPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loggedPodPath = r.URL.Path
		_, _ = w.Write([]byte("logs"))
	}))
	defer server.Close()
	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	params := investigateFailedPlanApplicationParams{
		Cluster: cluster, MachineName: testPlanNode, LogLineTail: 100,
		target: planTarget{nodeName: testPlanNodeName},
	}

	_, err = createJobAndPollPod(
		middleware.WithToken(t.Context(), testToken),
		params,
		logClient{toolsClient: tools.client, clientset: clientset},
		testToolboxImage,
		"",
		zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Contains(t, loggedPodPath, "/pods/"+currentPod.GetName()+"/log")
	assert.NotContains(t, loggedPodPath, stalePod.GetName())

	var createdJob *unstructured.Unstructured
	var deleteOptions *metav1.DeleteOptions
	for _, action := range dynClient.Actions() {
		if action.Matches("create", "jobs") {
			createdJob = action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		}
		if action.Matches("delete", "jobs") {
			options := action.(k8stesting.DeleteAction).GetDeleteOptions()
			deleteOptions = &options
		}
	}
	require.NotNil(t, createdJob)
	assert.Equal(t, jobPrefix, createdJob.GetGenerateName())
	assert.Empty(t, createdJob.GetName())
	require.NotNil(t, deleteOptions)
	require.NotNil(t, deleteOptions.PropagationPolicy)
	assert.Equal(t, metav1.DeletePropagationBackground, *deleteOptions.PropagationPolicy)
	require.NotNil(t, deleteOptions.Preconditions)
	require.NotNil(t, deleteOptions.Preconditions.UID)
	assert.Equal(t, types.UID(jobPrefix+"1"), *deleteOptions.Preconditions.UID)
}

func TestLogGathererFailedPod(t *testing.T) {
	const cluster = "c-error-pod-failed"
	pod := newLogGathererPod(testPlanNode, corev1.PodFailed)
	pod.Object["status"] = map[string]interface{}{
		"phase": "Failed",
		"containerStatuses": []interface{}{map[string]interface{}{
			"name": "journald-reader",
			"state": map[string]interface{}{"terminated": map[string]interface{}{
				"reason": "Error", "exitCode": int64(42), "message": "chroot failed",
			}},
		}},
	}
	dynClient := newPlanApplicationDynClient(newManagementCluster(cluster, true), pod)
	tools := newTestTools(dynClient)
	params := investigateFailedPlanApplicationParams{
		Cluster: cluster, MachineName: testPlanNode, LogLineTail: 100,
		target: planTarget{nodeName: testPlanNodeName},
	}
	_, err := createJobAndPollPod(middleware.WithToken(t.Context(), testToken), params, tools.client, testToolboxImage, "", zap.NewNop())
	require.Error(t, err)
	for _, part := range []string{"failed on node " + testPlanNodeName, "exit 42", "chroot failed", "fake logs"} {
		assert.ErrorContains(t, err, part)
	}
}

type logClient struct {
	toolsClient
	clientset kubernetes.Interface
}

func (c logClient) CreateClientSet(context.Context, string, string) (kubernetes.Interface, error) {
	return c.clientset, nil
}

func TestLogGathererPodLogAccessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "log access denied", http.StatusForbidden)
	}))
	defer server.Close()
	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)

	const cluster = "c-error-pod-logs"
	pod := newLogGathererPod(testPlanNode, corev1.PodFailed)
	pod.Object["status"] = map[string]interface{}{
		"phase": "Failed",
		"containerStatuses": []interface{}{map[string]interface{}{
			"name": "journald-reader",
			"state": map[string]interface{}{"terminated": map[string]interface{}{
				"reason": "Error", "exitCode": int64(42), "message": "chroot failed",
			}},
		}},
	}
	tools := newTestTools(newPlanApplicationDynClient(newManagementCluster(cluster, true), pod))
	params := investigateFailedPlanApplicationParams{
		Cluster: cluster, MachineName: testPlanNode, LogLineTail: 100,
		target: planTarget{nodeName: testPlanNodeName},
	}
	_, err = createJobAndPollPod(middleware.WithToken(t.Context(), testToken), params,
		logClient{toolsClient: tools.client, clientset: clientset}, testToolboxImage, "", zap.NewNop())
	require.Error(t, err)
	for _, part := range []string{"failed to retrieve logs", "log access denied", "phase Failed", "exit 42", "chroot failed", cluster} {
		assert.ErrorContains(t, err, part)
	}
}

func TestLogGathererDiagnosticsJobLookupError(t *testing.T) {
	dynClient := newPlanApplicationDynClient()
	dynClient.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("jobs forbidden")
	})
	details := nodeUtils.JobDiagnostics(t.Context(),
		dynClient.Resource(converter.K8sKindsToGVRs["job"]).Namespace(DefaultRancherResourcesNamespace),
		dynClient.Resource(converter.K8sKindsToGVRs["pod"]).Namespace(DefaultRancherResourcesNamespace),
		fake.NewClientset(), newLogGathererJob("reader"))
	assert.Contains(t, details, "could not read Job status: jobs forbidden")
	assert.Contains(t, details, "no Pod was created")
}
