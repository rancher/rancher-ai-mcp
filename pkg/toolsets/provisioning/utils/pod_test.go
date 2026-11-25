package utils

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

var podGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
var jobGVR = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}

func testPodClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList",
		jobGVR: "JobList",
	}, objects...)
}

func TestPollForPod(t *testing.T) {
	pod := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]interface{}{"name": "reader", "namespace": "cattle-system", "labels": map[string]interface{}{"job-name": "job"}},
		"status":     map[string]interface{}{"phase": "Succeeded"},
	}}
	for _, phase := range []corev1.PodPhase{corev1.PodSucceeded, corev1.PodFailed} {
		t.Run(string(phase), func(t *testing.T) {
			obj := pod.DeepCopy()
			obj.Object["status"] = map[string]interface{}{"phase": string(phase)}
			client := testPodClient(obj)
			got, err := PollForPod(t.Context(), "job-name=job", client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
			require.NoError(t, err)
			assert.Equal(t, phase, got.Status.Phase)
			assert.Equal(t, "reader", got.Name)
		})
	}

	t.Run("forbidden list preserves cause", func(t *testing.T) {
		client := testPodClient()
		forbidden := apierrors.NewForbidden(podGVR.GroupResource(), "", errors.New("list denied"))
		client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, forbidden
		})
		_, err := PollForPod(t.Context(), "job-name=job", client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
		require.ErrorIs(t, err, forbidden)
		assert.ErrorContains(t, err, "list denied")
		assert.True(t, apierrors.IsForbidden(err))
	})

	t.Run("transient failure retries", func(t *testing.T) {
		client := testPodClient(pod)
		attempts := 0
		client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			attempts++
			if attempts == 1 {
				return true, nil, apierrors.NewServiceUnavailable("temporarily unavailable")
			}
			return false, nil, nil
		})
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		got, err := PollForPod(ctx, "job-name=job", client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
		require.NoError(t, err)
		assert.Equal(t, "reader", got.Name)
		assert.Equal(t, 2, attempts)
	})

	t.Run("transient failure is retained on deadline", func(t *testing.T) {
		client := testPodClient()
		client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewServiceUnavailable("temporarily unavailable")
		})
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := PollForPod(ctx, "job-name=job", client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.ErrorContains(t, err, "temporarily unavailable")
	})

	t.Run("pending pod status is retained on deadline", func(t *testing.T) {
		obj := pod.DeepCopy()
		obj.Object["status"] = map[string]interface{}{
			"phase": "Pending",
			"containerStatuses": []interface{}{map[string]interface{}{
				"name": "reader",
				"state": map[string]interface{}{"waiting": map[string]interface{}{
					"reason": "ImagePullBackOff", "message": "image unavailable",
				}},
			}},
		}
		client := testPodClient(obj)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := PollForPod(ctx, "job-name=job", client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.ErrorContains(t, err, "reader (phase Pending)")
		assert.ErrorContains(t, err, "ImagePullBackOff")
		assert.ErrorContains(t, err, "image unavailable")
	})

	t.Run("malformed pod is not retried as a timeout", func(t *testing.T) {
		obj := pod.DeepCopy()
		obj.Object["status"] = map[string]interface{}{"phase": int64(5)}
		client := testPodClient(obj)
		_, err := PollForPod(t.Context(), "job-name=job", client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
		require.ErrorContains(t, err, "failed to decode pod")
		assert.NotErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestPollForJobPod(t *testing.T) {
	failedPod := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"name": "failed", "namespace": "cattle-system",
			"labels": map[string]interface{}{"job-name": "job"},
		},
		"status": map[string]interface{}{"phase": "Failed"},
	}}
	succeededPod := failedPod.DeepCopy()
	succeededPod.SetName("succeeded")
	succeededPod.Object["status"] = map[string]interface{}{"phase": "Succeeded"}
	job := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   map[string]interface{}{"name": "job", "namespace": "cattle-system"},
	}}
	terminalJob := job.DeepCopy()
	terminalJob.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{map[string]interface{}{"type": "Failed", "status": "True"}},
	}

	t.Run("waits for a successful retry after the first pod fails", func(t *testing.T) {
		client := testPodClient(job, failedPod)
		client.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
			require.NoError(t, client.Tracker().Add(succeededPod))
			return false, nil, nil
		})
		got, err := PollForJobPod(t.Context(),
			client.Resource(jobGVR).Namespace("cattle-system"), "job", "job-name=job",
			client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
		require.NoError(t, err)
		assert.Equal(t, "succeeded", got.Name)
	})

	t.Run("waits while a failed pod can still be retried", func(t *testing.T) {
		client := testPodClient(job, failedPod)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := PollForJobPod(ctx,
			client.Resource(jobGVR).Namespace("cattle-system"), "job", "job-name=job",
			client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.ErrorContains(t, err, "phase Failed")
	})

	t.Run("returns failed pod when the job has exhausted retries", func(t *testing.T) {
		client := testPodClient(terminalJob, failedPod)
		got, err := PollForJobPod(t.Context(),
			client.Resource(jobGVR).Namespace("cattle-system"), "job", "job-name=job",
			client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
		require.NoError(t, err)
		assert.Equal(t, "failed", got.Name)
	})

	t.Run("returns the newest failed pod when the job has exhausted retries", func(t *testing.T) {
		// the names are in the opposite order to the creation times, so neither order of the list is the newest
		for name, newestName := range map[string]string{"newest is listed first": "a-second-attempt", "newest is listed last": "z-second-attempt"} {
			t.Run(name, func(t *testing.T) {
				older, newer := failedPod.DeepCopy(), failedPod.DeepCopy()
				older.SetCreationTimestamp(metav1.NewTime(time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)))
				newer.SetCreationTimestamp(metav1.NewTime(time.Date(2026, 11, 25, 15, 1, 0, 0, time.UTC)))
				newer.SetName(newestName)
				if newestName == "a-second-attempt" {
					older.SetName("z-first-attempt")
				} else {
					older.SetName("a-first-attempt")
				}

				client := testPodClient(terminalJob, older, newer)
				got, err := PollForJobPod(t.Context(),
					client.Resource(jobGVR).Namespace("cattle-system"), "job", "job-name=job",
					client.Resource(podGVR).Namespace("cattle-system"), zap.NewNop())
				require.NoError(t, err)
				assert.Equal(t, newestName, got.Name)
			})
		}
	})
}

func TestPodStatusSummary(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "reader"},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
				Reason: "Unschedulable", Message: "node unavailable",
			}},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "image", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull", Message: "pull denied"}}},
				{Name: "reader", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 42, Message: "chroot failed"}}},
			},
		},
	}
	summary := PodStatusSummary(pod)
	for _, part := range []string{"reader (phase Failed)", "Unschedulable", "node unavailable", "ErrImagePull", "pull denied", "exit 42", "chroot failed"} {
		assert.Contains(t, summary, part)
	}
}
