package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	nodeUtils "github.com/rancher/rancher-ai-mcp/pkg/toolsets/provisioning/utils"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"github.com/rancher/rancher/pkg/plan"
	"github.com/rancher/wrangler/v3/pkg/name"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/dynamic"
)

type investigateFailedPlanApplicationParams struct {
	Cluster     string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	MachineName string `json:"machineName" jsonschema:"the CAPI Machine name of the machine failing to execute a plan. For imported clusters, identifies the machine in the log-gathering job name."`
	NodeName    string `json:"nodeName,omitempty" jsonschema:"the Kubernetes node name. Required for imported clusters; ignored for clusters with CAPI Machines."`
	LogLineTail int    `json:"logLineTail" jsonschema:"an optional number of log lines to retrieve from the system-agent logs, defaults to 150. Returns the last N lines of logs. Must be between 100 and 1000."`

	// target is the downstream node for MachineName and the location of its machine plan. It is resolved by validate.
	target planTarget
}

const defaultLogLineTail = 150

// minLogLineTail is large enough for the log gatherer's output to fit in the tail of the pod logs. That is a full match
// of the plan hash (the matching line with the 10 lines before and 15 lines after it), the system-agent's execution
// lines, and the service status.
const minLogLineTail = 100

var (
	// checksumRegex matches a plan checksum, which is a hex encoded sha256.
	checksumRegex = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// safePlanHashRegex matches the characters allowed in a plan hash which is placed in a script. It also allows the
	// placeholder used by plan previews.
	safePlanHashRegex = regexp.MustCompile(`^[0-9A-Za-z_]+$`)
)

func (params *investigateFailedPlanApplicationParams) validate(ctx context.Context, t *Tools, toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":     params.Cluster,
		"machineName": params.MachineName,
		"logLineTail": fmt.Sprintf("%d", params.LogLineTail),
	})

	log.Debug("investigateFailedPlanApplicationParams called with params", zap.Any("params", params))

	if params.Cluster == "local" {
		return nil, fmt.Errorf("plans are not used to control the local cluster")
	}

	if strings.TrimSpace(params.Cluster) == "" {
		return nil, fmt.Errorf("cluster name is required")
	}

	if strings.TrimSpace(params.MachineName) == "" {
		return nil, fmt.Errorf("machine name is required")
	}

	// explicitly allow a zero value and silently convert it to the default value. This handles
	// cases where the model omits the parameter entirely, with the intent of using default value as
	// described in the tool's schema.
	if (params.LogLineTail < minLogLineTail && params.LogLineTail != 0) || params.LogLineTail > 1000 {
		return nil, fmt.Errorf("logLineTail must be between %d and 1000", minLogLineTail)
	}

	if params.LogLineTail == 0 {
		params.LogLineTail = defaultLogLineTail
	}

	// the cluster may be given as a display name, so resolve it to the provisioning name (the CAPI cluster label)
	// and the management ID, which is used for all downstream access.
	provisioningName, clusterID, err := t.resolveMachineCluster(ctx, log, DefaultClusterResourcesNamespace, params.Cluster)
	if err != nil {
		return nil, err
	}
	if err := validateClusterName(provisioningName); err != nil {
		return nil, err
	}
	if clusterID == "" {
		return nil, fmt.Errorf("cluster %s has not been assigned a management cluster yet", params.Cluster)
	}
	if clusterID == "local" {
		return nil, fmt.Errorf("plans are not used to control the local cluster")
	}
	params.Cluster = clusterID

	// ensure that the provided machine name is actually valid for the specified cluster and
	// that it is managed by Rancher machine plans (i.e. CAPI compliant)
	machines, _, _, err := t.getAllCAPIMachineResources(ctx, log, getCAPIMachineResourcesParams{
		namespace:     DefaultClusterResourcesNamespace,
		targetCluster: provisioningName,
		kinds: &capiResourceKinds{
			machines: true,
		},
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to list CAPI machines for cluster %s: %w", params.Cluster, err)
	}

	// imported clusters have no CAPI machines. They are handled separately, and only when the
	// system-agent is expected to be running on them.
	if len(machines) == 0 {

		// confirms that the cluster is a 'generic' imported cluster (e.g. not aks,eks,gke,etc.)
		// and that the relevant features and settings are configured for imported day2ops.
		target, err := t.resolveImportedPlanTarget(ctx, log, params.Cluster, params.NodeName)
		if err != nil {
			return nil, err
		}

		params.target = target
		log = log.With(zap.Any("target", params.target))

		_, err = t.client.GetResource(ctx, client.GetParams{
			Cluster: params.Cluster,
			Kind:    "node",
			Name:    target.nodeName,
			Token:   middleware.Token(ctx),
		})
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("Kubernetes node %q does not exist in cluster %s; check the nodeName parameter", target.nodeName, params.Cluster)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to verify Kubernetes node %q in cluster %s: %w", target.nodeName, params.Cluster, err)
		}

		return log, nil
	}

	var machine *unstructured.Unstructured
	for _, m := range machines {
		if m.GetName() == params.MachineName {
			machine = m
			break
		}
	}

	if machine == nil {
		return nil, fmt.Errorf("failed to find machine %s in cluster %s; machineName must be the CAPI Machine name", params.MachineName, params.Cluster)
	}

	// the machine name is not the node name (e.g. custom clusters use the host's name), so resolve
	// the downstream node from the machine's node reference.
	nodeName, _, err := unstructured.NestedString(machine.Object, "status", "nodeRef", "name")
	if err != nil {
		return nil, fmt.Errorf("failed to read the node reference of machine %s: %w", params.MachineName, err)
	}

	if nodeName == "" {
		return nil, fmt.Errorf("machine %s has not registered a node in cluster %s yet, so there is no node to gather logs from", params.MachineName, params.Cluster)
	}

	params.target = planTarget{
		nodeName:            nodeName,
		planSecretNamespace: DefaultClusterResourcesNamespace,
		planSecretName:      fmt.Sprintf("%s-machine-plan", params.MachineName),
	}

	return log.With(zap.Any("target", params.target)), nil
}

func (t *Tools) investigateFailedPlanApplication(ctx context.Context, toolReq *mcp.CallToolRequest, params investigateFailedPlanApplicationParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(ctx, t, toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("retrieving machine plan for node")

	// get select fields from the machine plan for the requested node and the overall plan hash
	machinePlanFields, planHash, planErr := getMachinePlanFields(ctx, params.target, t.client, log)
	var responseOptions []response.Option
	if planErr != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		log.Warn("machine plan unavailable, retrieving recent system-agent logs", zap.Error(planErr))
		responseOptions = append(responseOptions, response.WithNotes(fmt.Sprintf("Machine plan unavailable: %v. Returned recent system-agent logs without filtering by plan hash.", planErr)))
	}

	log.Debug("deploying systemd log gatherer job to retrieve system-agent logs for node", zap.String("planHash", planHash))

	// Filter logs on the plan hash when available.
	logs, err := createJobAndPollPod(ctx, params, t.client, t.toolboxImage, planHash, log)
	if err != nil {
		log.Error("failed to collect system-agent logs or clean up the log-gatherer Job", zap.Error(err))
		if planErr != nil {
			return nil, nil, fmt.Errorf("machine plan unavailable: %w; failed to retrieve system-agent logs: %w", planErr, err)
		}
		return nil, nil, err
	}

	ulogs, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&logs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert logs: %w", err)
	}

	resources := []*unstructured.Unstructured{{Object: ulogs}}
	if planErr == nil {
		resources = append(resources, &unstructured.Unstructured{Object: machinePlanFields})
	}
	mcpResponse, err := response.CreateMcpResponse(resources, params.Cluster, responseOptions...)
	if err != nil {
		log.Error("failed to create mcp response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: mcpResponse,
		}},
	}, nil, nil
}

func createJobAndPollPod(ctx context.Context, params investigateFailedPlanApplicationParams, client toolsClient, image, planHash string, log *zap.Logger) (logs utils.ContainerLogs, err error) {
	jobManifest, err := buildFailedPlanLogGathererJob(params, image, planHash, log)
	if err != nil {
		log.Error("failed to create systemd job", zap.Error(err))
		return utils.ContainerLogs{}, err
	}

	ji, err := client.GetResourceInterface(ctx, middleware.Token(ctx), DefaultRancherResourcesNamespace, params.Cluster, converter.K8sKindsToGVRs["job"])
	if err != nil {
		log.Error("failed to get job resource", zap.Error(err))
		return utils.ContainerLogs{}, fmt.Errorf("failed to access log-gatherer Jobs in namespace %s on cluster %s: %w", DefaultRancherResourcesNamespace, params.Cluster, err)
	}

	log.Debug("creating log gatherer job", zap.String("generateName", jobManifest.GetGenerateName()), zap.String("namespace", jobManifest.GetNamespace()))

	job, err := ji.Create(ctx, jobManifest, metav1.CreateOptions{})
	if err != nil {
		log.Error("failed to create job resource", zap.Error(err))
		return utils.ContainerLogs{}, fmt.Errorf("failed to create log-gatherer Job with prefix %s in namespace %s on cluster %s for node %s: %w", jobManifest.GetGenerateName(), DefaultRancherResourcesNamespace, params.Cluster, params.target.nodeName, err)
	}

	// Always remove the Job, including on early error returns.
	defer func() {
		if cleanupErr := deleteLogGathererJob(ctx, ji, job); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to clean up log-gatherer Job %s in namespace %s on cluster %s: %w", job.GetName(), job.GetNamespace(), params.Cluster, cleanupErr))
		}
	}()

	pi, err := client.GetResourceInterface(ctx, middleware.Token(ctx), DefaultRancherResourcesNamespace, params.Cluster, converter.K8sKindsToGVRs["pod"])
	if err != nil {
		log.Error("failed to get pod resource", zap.Error(err))
		return utils.ContainerLogs{}, fmt.Errorf("failed to access Pods for log-gatherer Job %s in namespace %s on cluster %s: %w", job.GetName(), DefaultRancherResourcesNamespace, params.Cluster, err)
	}

	log.Debug("waiting for log gatherer pod to roll out", zap.String("jobName", job.GetName()), zap.String("namespace", job.GetNamespace()))

	clientset, err := client.CreateClientSet(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		return utils.ContainerLogs{}, fmt.Errorf("failed to create Kubernetes client for log-gatherer Job %s on cluster %s: %w", job.GetName(), params.Cluster, err)
	}

	// Allow time after the Job deadline for Kubernetes to report the final pod state.
	pollCtx, cancel := context.WithTimeout(ctx, nodeUtils.SystemdLogGathererJobDeadline+30*time.Second)
	selector := nodeUtils.JobPodSelector(job)
	pod, err := nodeUtils.PollForJobPod(pollCtx, ji, job.GetName(), selector, pi, log)
	cancel()
	if err != nil {
		details := nodeUtils.JobDiagnostics(ctx, ji, pi, clientset, job)
		return utils.ContainerLogs{}, fmt.Errorf("log-gatherer Job %s in namespace %s on cluster %s did not produce a completed Pod for node %s: %w%s", job.GetName(), job.GetNamespace(), params.Cluster, params.target.nodeName, err, details)
	}

	log.Debug("retrieving logs from log gatherer pod", zap.String("podName", pod.GetName()), zap.String("namespace", pod.GetNamespace()))

	// Read the logs from stdout
	logs, err = utils.GetPodLogs(ctx, clientset, pod, params.LogLineTail)
	if err != nil && len(logs.Logs) > 0 {
		// Keep the containers that were read successfully.
		log.Warn("partial log retrieval from log-gatherer pod", zap.Error(err))
		err = nil
	}
	if err != nil {
		return utils.ContainerLogs{}, fmt.Errorf("failed to retrieve logs from Pod %s for Job %s in namespace %s on cluster %s (pod status: %s): %w", pod.GetName(), job.GetName(), pod.GetNamespace(), params.Cluster, nodeUtils.PodStatusSummary(pod), err)
	}
	if pod.Status.Phase == corev1.PodFailed {
		err = fmt.Errorf("log-gatherer Pod %s for Job %s failed on node %s: %s; output: %v", pod.GetName(), job.GetName(), params.target.nodeName, nodeUtils.PodStatusSummary(pod), logs.Logs)
	}

	return logs, err
}

func deleteLogGathererJob(ctx context.Context, jobs dynamic.ResourceInterface, job *unstructured.Unstructured) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	options := metav1.DeleteOptions{PropagationPolicy: new(metav1.DeletePropagationBackground)}
	if uid := job.GetUID(); uid != "" {
		options.Preconditions = &metav1.Preconditions{UID: &uid}
	}
	err := jobs.Delete(cleanupCtx, job.GetName(), options)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func buildFailedPlanLogGathererJob(params investigateFailedPlanApplicationParams, image, planHash string, log *zap.Logger) (*unstructured.Unstructured, error) {
	journald := "journalctl -eu rancher-system-agent.service --no-pager -o cat"
	systemctl := "systemctl status rancher-system-agent.service -n 0; status=$?; if [ \"$status\" -ne 0 ]; then echo \"systemctl status returned exit code $status\"; fi; exit 0"
	logCommand := fmt.Sprintf("echo 'Machine plan unavailable; showing recent system-agent logs'; %s | tail -n %d", journald, params.LogLineTail)
	if planHash != "" {
		// the hash is interpolated into a script which runs on the node, so it must never contain anything but plain characters
		if !safePlanHashRegex.MatchString(planHash) {
			return nil, fmt.Errorf("invalid plan hash %q", planHash)
		}

		// The pod logs are tailed to LogLineTail, so the budget is split between the two sections, leaving some
		// room for the separator and the service status which follow.
		executionLines := params.LogLineTail / 2
		contextLines := params.LogLineTail - executionLines - 10

		// A plan which keeps failing may log its checksum many times, pushing the application out of the tailed
		// output. So the system-agent's execution lines are emitted last. Not all of them mention the checksum, so
		// everything from the start of the latest application onward is kept. That includes the output of its
		// instructions, which the system-agent logs with the prefix [<checksum>_<instruction index>:stdout or :stderr].
		applyinator := fmt.Sprintf("%s | awk '%s' | grep -F -e '[Applyinator]' -e 'error executing instruction' -e '[%s_' | head -n %d", journald, fromLastApplication(planHash), planHash, executionLines)
		logCommand = fmt.Sprintf("hits=$(%s | grep %s -A 15 -B 10 | tail -n %d); if [ -n \"$hits\" ]; then printf '%%s\\n' \"$hits\"; echo '--- system-agent execution lines for the machine plan hash ---'; %s; else echo 'No logs found for the machine plan hash; showing the last 25 lines of service logs currently available'; %s | tail -n 25; fi", journald, planHash, contextLines, applyinator, journald)
	}
	journaldCommand := []string{
		"chroot",
		"/host",
		"bash",
		"-c",
		fmt.Sprintf("%s; %s", logCommand, systemctl),
	}

	job, err := nodeUtils.CreateSystemdLogGathererJob("", params.target.nodeName, "rancher-system-agent", image, log, journaldCommand...)
	if err != nil {
		log.Error("failed to create systemd job", zap.Error(err))
		return nil, err
	}

	job.SetGenerateName(name.SafeConcatName(params.MachineName, "systemd-log-gatherer") + "-")
	return job, nil
}

// maxExecutionScanLines bounds how many lines of the journal are held in memory when looking for the execution.
const maxExecutionScanLines = 5000

// fromLastApplication returns an awk program which prints the journal starting from the latest line announcing the
// application of the plan, or from the first mention of the hash if no such line is in the journal. Only the first
// maxExecutionScanLines lines of that are kept, so that the memory used does not grow with a long running node's journal.
// The hash must already be validated, as it is placed in a script.
func fromLastApplication(planHash string) string {
	return fmt.Sprintf("/%[1]s/ && !seen {seen=1} /Applying one-time instructions for plan with checksum %[1]s/ {split(\"\", l); n=0; seen=1} seen && n < %[2]d {l[++n]=$0} END {for (i=1; i<=n; i++) print l[i]}", planHash, maxExecutionScanLines)
}

func getMachinePlanFields(ctx context.Context, target planTarget, c toolsClient, log *zap.Logger) (map[string]interface{}, string, error) {
	var machinePlanUnstruct *unstructured.Unstructured
	var err error

	// Imported nodes do not use the CAPI machine name when naming the machine plan secret.
	// Instead, the cluster and node names are tracked as labels on the secret.
	if target.imported {
		clusterRequirement, err := labels.NewRequirement(importedPlanClusterLabel, selection.Equals, []string{target.planSecretNamespace})
		if err != nil {
			return nil, "", fmt.Errorf("invalid cluster ID for imported machine plan lookup: %w", err)
		}
		nodeRequirement, err := labels.NewRequirement(importedPlanNodeLabel, selection.Equals, []string{target.nodeName})
		if err != nil {
			return nil, "", fmt.Errorf("invalid node name for imported machine plan lookup: %w", err)
		}
		selector := labels.NewSelector().Add(*clusterRequirement, *nodeRequirement).String()
		secrets, err := c.GetResources(ctx, client.ListParams{
			Cluster:       LocalCluster,
			Kind:          "secret",
			Namespace:     target.planSecretNamespace,
			LabelSelector: selector,
			Token:         middleware.Token(ctx),
		})
		if err != nil {
			return nil, "", fmt.Errorf("failed to list imported machine plans for node %s: %w", target.nodeName, err)
		}

		for _, secret := range secrets {
			secretType, _, err := unstructured.NestedString(secret.Object, "type")
			if err != nil {
				return nil, "", fmt.Errorf("failed to read type of secret %s: %w", secret.GetName(), err)
			}
			if secretType != machinePlanSecretType {
				continue
			}
			if machinePlanUnstruct != nil {
				return nil, "", fmt.Errorf("multiple machine plans found for node %s in cluster %s", target.nodeName, target.planSecretNamespace)
			}
			machinePlanUnstruct = secret
		}
		if machinePlanUnstruct == nil {
			return nil, "", fmt.Errorf("machine plan not found for node %s in cluster %s", target.nodeName, target.planSecretNamespace)
		}
	} else {
		si, err := c.GetResourceInterface(ctx, middleware.Token(ctx), target.planSecretNamespace, "local", converter.K8sKindsToGVRs["secret"])
		if err != nil {
			log.Error("failed to get secret resource interface", zap.Error(err))
			return nil, "", fmt.Errorf("failed to access machine plan secret %s in namespace %s: %w", target.planSecretName, target.planSecretNamespace, err)
		}

		machinePlanUnstruct, err = si.Get(ctx, target.planSecretName, metav1.GetOptions{})
		if err != nil {
			log.Error("failed to get machine plan resource", zap.Error(err))
			return nil, "", fmt.Errorf("failed to get machine plan secret %s in namespace %s: %w", target.planSecretName, target.planSecretNamespace, err)
		}
	}

	machinePlan := &corev1.Secret{}
	j, err := machinePlanUnstruct.MarshalJSON()
	if err != nil {
		log.Error("failed to marshal machine plan resource interface", zap.Error(err))
		return nil, "", fmt.Errorf("failed to encode machine plan secret %s: %w", machinePlanUnstruct.GetName(), err)
	}

	err = json.Unmarshal(j, &machinePlan)
	if err != nil {
		log.Error("failed to unmarshal machine plan resource interface", zap.Error(err))
		return nil, "", fmt.Errorf("failed to decode machine plan secret %s: %w", machinePlanUnstruct.GetName(), err)
	}

	// A machine-plan may have its plan instructions removed by the orchestrator after a failed
	// application, however its other details are still useful for understanding the recent failures.
	currentPlanChecksum := ""
	if len(machinePlan.Data["plan"]) != 0 {
		currentPlanChecksum = plan.Checksum(machinePlan.Data["plan"])
	}

	// The system-agent tracks the checksum of the last plan which failed. When the plan has failed, or has been removed,
	// that is the one to investigate. Otherwise the orchestrator has delivered a newer plan since, which is the one being
	// applied now. The value is untrusted, as it is used to filter logs on the node, so only accept what a checksum
	// looks like.
	failedChecksum := string(machinePlan.Data["failed-checksum"])
	validFailedChecksum := checksumRegex.MatchString(failedChecksum)
	if failedChecksum != "" && !validFailedChecksum {
		log.Warn("ignoring malformed failed-checksum in machine plan resource")
		if currentPlanChecksum == "" {
			return nil, "", fmt.Errorf("no plan found in machine plan resource, and its failed-checksum is not a valid checksum")
		}
	}

	planState := string(machinePlan.Data["plan-state"])
	planHasFailed := planState == "" || planState == "failed"

	checksum := currentPlanChecksum
	checksumNote := "The checksum is of the current plan. The system-agent has not recorded a failed plan checksum."
	switch {
	case failedChecksum != "" && !validFailedChecksum:
		checksumNote = "The checksum is of the current plan. The failed-checksum recorded by the system-agent is not a valid checksum, so it was ignored."
	case validFailedChecksum && currentPlanChecksum != "" && !planHasFailed:
		checksumNote = "The checksum is of the current plan, which is not in a failed state. A plan failed before it, but that plan is not shown."
		if failedChecksum == currentPlanChecksum {
			checksumNote = "The checksum is of the current plan, which failed before and is not in a failed state now."
		}
	case validFailedChecksum:
		checksum = failedChecksum
		switch currentPlanChecksum {
		case "":
			checksumNote = "The checksum is of the last failed plan, as recorded by the system-agent. The plan has been removed from the machine plan."
		case failedChecksum:
			checksumNote = "The checksum is of the last failed plan, as recorded by the system-agent. It is also the current plan."
		default:
			checksumNote = fmt.Sprintf("The checksum is of the last failed plan, as recorded by the system-agent. The current plan has a different checksum (%s), so it has not been applied yet or has not failed since.", currentPlanChecksum)
			if planState == "failed" {
				checksumNote = fmt.Sprintf("The checksum is of the last failed plan, as recorded by the system-agent. The plan state is failed, but the current plan has a different checksum (%s), so the failure may be of an earlier plan.", currentPlanChecksum)
			}
		}
	}

	if checksum == "" {
		return nil, "", fmt.Errorf("no plan found in machine plan resource")
	}

	out := map[string]string{
		"checksum":      checksum,
		"checksum-note": checksumNote,
		// only a value which looks like a checksum is reported, as the secret is not trusted
		"failed-checksum": "",
	}
	if validFailedChecksum {
		out["failed-checksum"] = failedChecksum
	}
	for _, field := range []string{
		"failure-count",
		"last-apply-time",
		"max-failures",
		"plan-revision",
		"plan-state",
		"probe-statuses",
	} {
		out[field] = string(machinePlan.Data[field])
	}

	uFields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
	if err != nil {
		return nil, "", fmt.Errorf("failed to convert machine plan fields: %w", err)
	}

	return uFields, checksum, nil
}
