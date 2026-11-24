package utils

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// JobPodSelector isolates a Job's Pods from earlier runs, falling back to its name when no UID is available.
func JobPodSelector(job *unstructured.Unstructured) string {
	if uid := job.GetUID(); uid != "" {
		return "batch.kubernetes.io/controller-uid=" + string(uid)
	}
	return "job-name=" + job.GetName()
}

// JobDiagnostics describes Job conditions, Pod states, and events, including lookup failures.
// The resource interfaces must be scoped to the Job's namespace.
// Reads are bounded to three seconds even when the caller's context has expired.
func JobDiagnostics(ctx context.Context, jobs, pods dynamic.ResourceInterface, clientset kubernetes.Interface, job *unstructured.Unstructured) string {
	diagnosticCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()

	var details []string
	jobName := job.GetName()
	currentJob, err := jobs.Get(diagnosticCtx, jobName, metav1.GetOptions{})
	if err != nil {
		details = append(details, fmt.Sprintf("could not read Job status: %v", err))
	} else if conditions, found, _ := unstructured.NestedSlice(currentJob.Object, "status", "conditions"); found {
		for _, raw := range conditions {
			if condition, ok := raw.(map[string]interface{}); ok {
				detail := fmt.Sprintf("Job condition %v=%v", condition["type"], condition["status"])
				for _, field := range []string{"reason", "message"} {
					if value, ok := condition[field].(string); ok && value != "" {
						detail += ": " + value
					}
				}
				details = append(details, detail)
			}
		}
	}

	podList, err := pods.List(diagnosticCtx, metav1.ListOptions{LabelSelector: JobPodSelector(job)})
	var podNames []string
	if err != nil {
		details = append(details, fmt.Sprintf("could not read Job Pods: %v", err))
	} else if len(podList.Items) == 0 {
		details = append(details, "no Pod was created for the Job")
	} else {
		for _, item := range podList.Items {
			var pod corev1.Pod
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, &pod); err != nil {
				details = append(details, fmt.Sprintf("could not decode Pod %s status: %v", item.GetName(), err))
				continue
			}
			podNames = append(podNames, pod.Name)
			details = append(details, "Pod "+PodStatusSummary(pod))
		}
	}

	for _, objectName := range append([]string{jobName}, podNames...) {
		events, err := clientset.CoreV1().Events(job.GetNamespace()).List(diagnosticCtx, metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("involvedObject.name", objectName).String(),
		})
		if err != nil {
			details = append(details, fmt.Sprintf("could not read events for %s: %v", objectName, err))
			continue
		}
		for _, event := range events.Items {
			details = append(details, fmt.Sprintf("event %s for %s: %s", event.Reason, objectName, event.Message))
		}
	}

	if len(details) == 0 {
		return ""
	}
	return "; diagnostics: " + strings.Join(details, "; ")
}
