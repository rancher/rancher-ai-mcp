package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

// PollForPod polls for a pod with the given label selector until it is found or the context times out. It returns the pod if found, or an error if not found or if there was an error during polling.
// It polls for at most 20 seconds, checking every 1 second. If the pod is found, it checks if the pod's status phase is either Succeeded or Failed before returning it.
func PollForPod(ctx context.Context, labelSelector string, podResourceInterface dynamic.ResourceInterface, log *zap.Logger) (corev1.Pod, error) {
	var pod *corev1.Pod
	err := wait.PollUntilContextTimeout(ctx, time.Second*1, time.Second*20, true, func(ctx context.Context) (done bool, err error) {
		pods, err := podResourceInterface.List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			log.Error("failed to list pods", zap.Error(err))
			return false, nil
		}

		if len(pods.Items) == 0 {
			return false, nil
		}

		j, err := pods.Items[0].MarshalJSON()
		if err != nil {
			log.Error("failed to marshal pod", zap.Error(err))
			return false, nil
		}

		err = json.Unmarshal(j, &pod)
		if err != nil {
			log.Error("failed to unmarshal pod", zap.Error(err))
			return false, nil
		}

		return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed, nil
	})
	if err != nil {
		log.Error("failed to poll for pods", zap.Error(err))
		return corev1.Pod{}, err
	}

	if pod == nil {
		log.Error("failed to find pod", zap.String("selector", labelSelector))
		return corev1.Pod{}, fmt.Errorf("failed to find pod")
	}

	return *pod, nil
}
