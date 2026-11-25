package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

// PollForPod waits for a matching pod to succeed or fail. The caller controls the deadline
// and decides whether a failed pod's output is usable.
func PollForPod(ctx context.Context, labelSelector string, podResourceInterface dynamic.ResourceInterface, log *zap.Logger) (corev1.Pod, error) {
	return pollForPod(ctx, labelSelector, podResourceInterface, nil, "", log)
}

// PollForJobPod waits for a successful Pod or for its Job to reach a terminal Failed condition.
func PollForJobPod(ctx context.Context, jobs dynamic.ResourceInterface, jobName, labelSelector string, pods dynamic.ResourceInterface, log *zap.Logger) (corev1.Pod, error) {
	return pollForPod(ctx, labelSelector, pods, jobs, jobName, log)
}

func pollForPod(ctx context.Context, labelSelector string, pods, jobs dynamic.ResourceInterface, jobName string, log *zap.Logger) (corev1.Pod, error) {
	var pod *corev1.Pod
	var lastAPIError error

	err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (done bool, err error) {
		podList, err := pods.List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			log.Error("failed to list pods", zap.Error(err))
			lastAPIError = err
			if isRetryablePodPollError(err) {
				return false, nil
			}
			return false, err
		}

		if len(podList.Items) == 0 {
			return false, nil
		}

		var failedPod *corev1.Pod
		for i := range podList.Items {
			var candidate corev1.Pod
			j, err := podList.Items[i].MarshalJSON()
			if err != nil {
				log.Error("failed to marshal pod", zap.Error(err))
				return false, fmt.Errorf("failed to marshal pod matching %q: %w", labelSelector, err)
			}
			if err := json.Unmarshal(j, &candidate); err != nil {
				log.Error("failed to unmarshal pod", zap.Error(err))
				return false, fmt.Errorf("failed to decode pod matching %q: %w", labelSelector, err)
			}
			pod = &candidate
			if candidate.Status.Phase == corev1.PodSucceeded {
				return true, nil
			}
			// the Job may retry, so the latest failed pod is the one which explains the terminal failure.
			if candidate.Status.Phase == corev1.PodFailed && (failedPod == nil || candidate.CreationTimestamp.After(failedPod.CreationTimestamp.Time)) {
				failedPod = &candidate
			}
		}

		if failedPod == nil {
			return false, nil
		}
		if jobs == nil {
			pod = failedPod
			return true, nil
		}

		job, err := jobs.Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			log.Error("failed to get Job status while polling", zap.Error(err))
			lastAPIError = err
			if isRetryablePodPollError(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to read Job %s status: %w", jobName, err)
		}
		conditions, found, err := unstructured.NestedSlice(job.Object, "status", "conditions")
		if err != nil {
			return false, fmt.Errorf("failed to read Job %s conditions: %w", jobName, err)
		}
		if found && hasFailedJobCondition(conditions) {
			pod = failedPod
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		log.Error("failed to poll for pods", zap.Error(err))
		details := ""
		if pod != nil {
			details = "; latest pod: " + PodStatusSummary(*pod)
		}
		if lastAPIError != nil {
			details += fmt.Sprintf("; last API error: %v", lastAPIError)
		}
		return corev1.Pod{}, fmt.Errorf("waiting for pod matching %q failed%s: %w", labelSelector, details, err)
	}

	if pod == nil {
		log.Error("failed to find pod", zap.String("selector", labelSelector))
		return corev1.Pod{}, fmt.Errorf("no pod appeared matching %q before the wait timed out", labelSelector)
	}

	return *pod, nil
}

func isRetryablePodPollError(err error) bool {
	return apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err)
}

func hasFailedJobCondition(conditions []interface{}) bool {
	for _, raw := range conditions {
		condition, ok := raw.(map[string]interface{})
		if ok && condition["type"] == "Failed" && condition["status"] == "True" {
			return true
		}
	}
	return false
}

// PodStatusSummary describes the scheduling and container state that can explain why a pod failed to start.
func PodStatusSummary(pod corev1.Pod) string {
	summary := fmt.Sprintf("%s (phase %s)", pod.Name, pod.Status.Phase)
	var details []string
	for _, condition := range pod.Status.Conditions {
		if condition.Status == corev1.ConditionFalse || condition.Reason != "" || condition.Message != "" {
			details = append(details, fmt.Sprintf("%s=%s: %s", condition.Type, condition.Reason, condition.Message))
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if waiting := status.State.Waiting; waiting != nil {
			details = append(details, fmt.Sprintf("container %s waiting: %s: %s", status.Name, waiting.Reason, waiting.Message))
		}
		if terminated := status.State.Terminated; terminated != nil {
			details = append(details, fmt.Sprintf("container %s terminated: %s (exit %d): %s", status.Name, terminated.Reason, terminated.ExitCode, terminated.Message))
		}
	}
	if len(details) > 0 {
		summary += "; " + fmt.Sprint(details)
	}
	return summary
}
