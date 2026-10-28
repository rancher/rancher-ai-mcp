package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	nodeUtils "github.com/rancher/rancher-ai-mcp/pkg/toolsets/provisioning/utils"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"github.com/rancher/rancher/pkg/plan"
	"github.com/rancher/wrangler/v3/pkg/name"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type investigateFailedPlanApplicationParams struct {
	Cluster     string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	NodeName    string `json:"nodeName" jsonschema:"the name of the node which is failing to execute a plan"`
	LogLineTail int    `json:"logLineTail" jsonschema:"an optional number of log lines to retrieve from the system-agent logs, defaults to 150. Returns the last N lines of logs. Must be between 30 and 1000."`
}

const defaultLogLineTail = 150

func (params *investigateFailedPlanApplicationParams) validate(ctx context.Context, t *Tools, toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":     params.Cluster,
		"nodeName":    params.NodeName,
		"logLineTail": fmt.Sprintf("%d", params.LogLineTail),
	})

	log.Debug("investigateFailedPlanApplicationParams called with params", zap.Any("params", params))

	if params.Cluster == "local" {
		return nil, fmt.Errorf("plans are not used to control the local cluster")
	}

	if strings.TrimSpace(params.NodeName) == "" {
		return nil, fmt.Errorf("node name is required")
	}

	// explicitly allow a zero value and silently convert it to the default value. This handles
	// cases where the model omits the parameter entirely, with the intent of using default value as
	// described in the tool's schema.
	if (params.LogLineTail < 30 && params.LogLineTail != 0) || params.LogLineTail > 1000 {
		return nil, fmt.Errorf("logLineTail must be between 30 and 1000")
	}

	if params.LogLineTail == 0 {
		params.LogLineTail = defaultLogLineTail
	}

	// ensure that the provided node name is actually valid for the specified cluster and
	// that it is managed by Rancher machine plans (i.e. CAPI compliant)
	machines, _, _, err := t.getAllCAPIMachineResources(ctx, log, getCAPIMachineResourcesParams{
		namespace:     DefaultClusterResourcesNamespace,
		targetCluster: params.Cluster,
		kinds: &capiResourceKinds{
			machines: true,
		},
	})
	if err != nil && !errors.IsNotFound(err) {
		return nil, fmt.Errorf("did not find any CAPI machine resources for cluster %s. This cluster is likely not managed by Rancher machine plans or the system agent. This tool cannot be run against clusters which do not utilize CAPI (aks,eks,gke,k3k,etc.): %w", params.Cluster, err)
	}

	if len(machines) == 0 {
		return nil, fmt.Errorf("did not find any CAPI machine resources for cluster %s. This cluster is likely not managed by Rancher machine plans or the system agent. This tool cannot be run against clusters which do not utilize CAPI (aks,eks,gke,k3k,etc.)", params.Cluster)
	}

	found := false
	for _, machine := range machines {
		if machine.GetName() == params.NodeName {
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("failed to find node %s", params.NodeName)
	}

	return log, nil
}

func (t *Tools) investigateFailedPlanApplication(ctx context.Context, toolReq *mcp.CallToolRequest, params investigateFailedPlanApplicationParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(ctx, t, toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("retrieving machine plan for node")

	// get select fields from the machine plan for the requested node and the overall plan hash
	machinePlanFields, planHash, err := getMachinePlanFields(ctx, params.NodeName, t.client, log)
	if err != nil {
		log.Error("failed to get machine plan", zap.Error(err))
		return nil, nil, err
	}

	log.Debug("deploying systemd log gatherer job to retrieve system-agent logs for node", zap.String("planHash", planHash))

	// get system-agent journald logs from the downstream node, filtering on the current machine
	// plan hash.
	logs, err := createJobAndPollPod(ctx, params, t.client, t.toolboxImage, planHash, log)
	if err != nil {
		log.Error("failed to create job", zap.Error(err))
		return nil, nil, err
	}

	ulogs, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&logs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert logs: %w", err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{
		{Object: ulogs},
		{Object: machinePlanFields}}, params.Cluster)
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

func createJobAndPollPod(ctx context.Context, params investigateFailedPlanApplicationParams, client toolsClient, image, planHash string, log *zap.Logger) (utils.ContainerLogs, error) {
	jobManifest, err := buildFailedPlanLogGathererJob(params, image, planHash, log)
	if err != nil {
		log.Error("failed to create systemd job", zap.Error(err))
		return utils.ContainerLogs{}, err
	}

	ji, err := client.GetResourceInterface(ctx, middleware.Token(ctx), DefaultRancherResourcesNamespace, params.Cluster, converter.K8sKindsToGVRs["job"])
	if err != nil {
		log.Error("failed to get job resource", zap.Error(err))
		return utils.ContainerLogs{}, err
	}

	log.Debug("creating log gathered job", zap.String("jobName", jobManifest.GetName()), zap.String("namespace", jobManifest.GetNamespace()))

	job, err := ji.Create(ctx, jobManifest, metav1.CreateOptions{})
	if err != nil {
		log.Error("failed to create job resource", zap.Error(err))
		return utils.ContainerLogs{}, err
	}

	pi, err := client.GetResourceInterface(ctx, middleware.Token(ctx), DefaultRancherResourcesNamespace, params.Cluster, converter.K8sKindsToGVRs["pod"])
	if err != nil {
		log.Error("failed to get pod resource", zap.Error(err))
		return utils.ContainerLogs{}, err
	}

	log.Debug("waiting for log gatherer pod to roll out", zap.String("jobName", job.GetName()), zap.String("namespace", job.GetNamespace()))

	// Wait for the jobs pod to roll out
	pod, err := nodeUtils.PollForPod(ctx, fmt.Sprintf("%s=%s", "job-name", job.GetName()), pi, log)
	if err != nil {
		return utils.ContainerLogs{}, err
	}

	clientset, err := client.CreateClientSet(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		return utils.ContainerLogs{}, fmt.Errorf("failed to create clientset: %w", err)
	}

	log.Debug("retrieving logs from log gatherer pod", zap.String("podName", pod.GetName()), zap.String("namespace", pod.GetNamespace()))

	// Read the logs from stdout
	logs, err := utils.GetPodLogs(ctx, clientset, pod, params.LogLineTail)
	if err != nil {
		return utils.ContainerLogs{}, fmt.Errorf("failed to get pod logs: %w", err)
	}

	return logs, nil
}

func buildFailedPlanLogGathererJob(params investigateFailedPlanApplicationParams, image, planHash string, log *zap.Logger) (*unstructured.Unstructured, error) {
	journald := "journalctl -eu rancher-system-agent.service --no-pager -o cat"
	grep := fmt.Sprintf("grep %s -A 15 -B 10", planHash)
	systemctl := "systemctl status rancher-system-agent.service -n 0; status=$?; if [ \"$status\" -eq 3 ]; then echo 'Service is inactive or failed'; exit 0; fi; exit \"$status\""
	logCommand := fmt.Sprintf("if ! %s | %s; then echo 'No logs found for the machine plan hash; showing the last 25 lines of service logs currently available'; %s | tail -n 25; fi", journald, grep, journald)
	journaldCommand := []string{
		"chroot",
		"/host",
		"bash",
		"-c",
		fmt.Sprintf("%s; %s", logCommand, systemctl),
	}

	job, err := nodeUtils.CreateSystemdLogGathererJob(fmt.Sprintf("%s-systemd-log-gatherer", name.SafeConcatName(params.NodeName)), params.NodeName, "rancher-system-agent", image, log, journaldCommand...)
	if err != nil {
		log.Error("failed to create systemd job", zap.Error(err))
		return nil, err
	}

	return job, err
}

func getMachinePlanFields(ctx context.Context, nodeName string, client toolsClient, log *zap.Logger) (map[string]interface{}, string, error) {
	si, err := client.GetResourceInterface(ctx, middleware.Token(ctx), DefaultClusterResourcesNamespace, "local", converter.K8sKindsToGVRs["secret"])
	if err != nil {
		log.Error("failed to get secret resource interface", zap.Error(err))
		return nil, "", err
	}

	machinePlanUnstruct, err := si.Get(ctx, fmt.Sprintf("%s-machine-plan", nodeName), metav1.GetOptions{})
	if err != nil {
		log.Error("failed to get machine plan resource interface", zap.Error(err))
		return nil, "", err
	}

	machinePlan := &corev1.Secret{}
	j, err := machinePlanUnstruct.MarshalJSON()
	if err != nil {
		log.Error("failed to marshal machine plan resource interface", zap.Error(err))
		return nil, "", err
	}

	err = json.Unmarshal(j, &machinePlan)
	if err != nil {
		log.Error("failed to unmarshal machine plan resource interface", zap.Error(err))
		return nil, "", err
	}

	if machinePlan.Data == nil || machinePlan.Data["plan"] == nil {
		return nil, "", fmt.Errorf("no plan found in machine plan resource")
	}

	currentPlanChecksum := plan.Checksum(machinePlan.Data["plan"])
	if currentPlanChecksum == "" {
		log.Error("failed to calculate checksum of plan resource", zap.Error(err))
	}

	out := map[string]string{
		"checksum": currentPlanChecksum,
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
		return nil, "", fmt.Errorf("failed to convert logs: %w", err)
	}

	return uFields, currentPlanChecksum, nil
}
