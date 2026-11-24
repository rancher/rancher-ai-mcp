package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type ContainerLogs struct {
	Logs map[string]any `json:"logs"`
}

func GetPodLogs(ctx context.Context, clientset kubernetes.Interface, pod corev1.Pod, tailLogLines int) (ContainerLogs, error) {
	logs := ContainerLogs{
		Logs: make(map[string]any),
	}
	var logErrors []error
	for _, container := range pod.Spec.Containers {
		podLogOptions := corev1.PodLogOptions{
			TailLines: new(int64(tailLogLines)),
			Container: container.Name,
		}
		req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &podLogOptions)
		podLogs, err := req.Stream(ctx)
		if err != nil {
			zap.L().Warn("unable to retrieve logs for container",
				zap.String("container", container.Name),
				zap.String("pod", pod.Name),
				zap.Error(err))
			logErrors = append(logErrors, fmt.Errorf("container %s: unable to retrieve logs: %w", container.Name, err))
			continue
		}
		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, podLogs)
		if err != nil {
			zap.L().Warn("failed to copy log stream",
				zap.String("container", container.Name),
				zap.String("pod", pod.Name),
				zap.Error(err))
			logErrors = append(logErrors, fmt.Errorf("container %s: failed to read logs: %w", container.Name, err))
			if err := podLogs.Close(); err != nil {
				zap.L().Warn("failed to close pod logs stream", zap.Error(err))
			}
			continue
		}
		logs.Logs[container.Name] = buf.String()
		if err := podLogs.Close(); err != nil {
			zap.L().Warn("failed to close pod logs stream", zap.Error(err))
			logErrors = append(logErrors, fmt.Errorf("container %s: failed to close logs stream: %w", container.Name, err))
		}
	}

	if len(logErrors) > 0 {
		return logs, fmt.Errorf("failed to retrieve all container logs: %w", errors.Join(logErrors...))
	}
	return logs, nil
}
