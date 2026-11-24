package utils

import (
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SystemdLogGathererJobDeadline bounds the lifetime of a log-gathering Job.
const SystemdLogGathererJobDeadline = 3 * time.Minute

// CreateSystemdLogGathererJob builds a job which reads the journal of a systemd unit on a single node.
// nodeName must be the name of the Kubernetes Node object. It is not the hostname, which is not
// guaranteed to match or to be unique, and it is not the CAPI machine name.
func CreateSystemdLogGathererJob(jobName, nodeName, unit, image string, log *zap.Logger, command ...string) (*unstructured.Unstructured, error) {
	defaultCommand := []string{
		"chroot",
		"/host",
		"journalctl",
		"-eu",
		fmt.Sprintf("%s.service", unit),
		"-n",
		"75",
		"--no-pager",
	}

	if len(command) == 0 {
		command = defaultCommand
	}

	job := &batchv1.Job{
		APIVersion: "batch/v1",
		Kind:       "Job",
		Name:       jobName,
		// as this job retrieves sensitive information it should only ever be
		// deployed into privileged namespaces.
		Namespace: "cattle-system",
		Spec: batchv1.JobSpec{
			BackoffLimit:            new(int32(1)),
			Completions:             new(int32(1)),
			TTLSecondsAfterFinished: new(int32(120)),
			ActiveDeadlineSeconds:   new(int64(SystemdLogGathererJobDeadline / time.Second)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "journald-reader",
							Image:   image,
							Command: command,
							SecurityContext: &corev1.SecurityContext{
								RunAsUser:                new(int64(0)),
								AllowPrivilegeEscalation: new(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{corev1.Capability("ALL")},
									Add:  []corev1.Capability{corev1.Capability("SYS_CHROOT")},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "host-root",
									MountPath: "/host",
									ReadOnly:  true,
								},
							},
						},
					},
					// Bind directly to the node instead of going through the scheduler. The node name is the
					// only identifier present and unique on every cluster type, and skipping the scheduler means
					// the job still runs when the scheduler is unavailable or the node is tainted or cordoned.
					// Those are common states for the nodes being investigated.
					NodeName: nodeName,
					// nodeName bypasses scheduling taints, but the kubelet still evicts pods which do not
					// tolerate NoExecute taints (e.g. not-ready).
					Tolerations: []corev1.Toleration{
						{Operator: corev1.TolerationOpExists},
					},
					Volumes: []corev1.Volume{
						{
							Name: "host-root",
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/",
								Type: new(corev1.HostPathDirectory),
							},
						},
					},
				},
			},
		},
	}

	objBytes, err := json.Marshal(job)
	if err != nil {
		log.Error("failed to marshal resource", zap.Error(err))
		return nil, fmt.Errorf("failed to marshal resource: %w", err)
	}

	unstructuredObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(objBytes, unstructuredObj); err != nil {
		log.Error("failed to create unstructured resource", zap.Error(err))
		return nil, fmt.Errorf("failed to create unstructured object: %w", err)
	}

	return unstructuredObj, nil
}
