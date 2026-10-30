package provisioning

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type inspectClusterMachinesParams struct {
	Cluster                   string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Namespace                 string `json:"namespace,omitempty" jsonschema:"the namespace where the resource is located. The default namespace will be used if not provided"`
	IncludeMachineDeployments bool   `json:"includeMachineDeployments" jsonschema:"set to true to return the cluster's CAPI MachineDeployments, which summarize desired and ready replicas for each machine pool"`
	IncludeMachineSets        bool   `json:"includeMachineSets" jsonschema:"set to true to return the cluster's CAPI MachineSets, which sit between MachineDeployments and Machines and reflect rollout state"`
	IncludeMachines           bool   `json:"includeMachines" jsonschema:"set to true to return the cluster's individual CAPI Machines, which carry per-node phase, conditions, and failure details"`
}

func (params *inspectClusterMachinesParams) validate(toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
	if params.Namespace == "" {
		params.Namespace = "fleet-default"
	}
	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":   params.Cluster,
		"namespace": params.Namespace,
	})
	if err := validateClusterName(params.Cluster); err != nil {
		log.Error("invalid cluster name")
		return nil, err
	}
	if !params.IncludeMachineDeployments && !params.IncludeMachineSets && !params.IncludeMachines {
		return nil, fmt.Errorf("at least one of includeMachineDeployments, includeMachineSets, or includeMachines must be true")
	}
	return log, nil
}

// analyzeClusterMachines returns the selected cluster API machines, machine sets, and machine deployments for a given provisioning cluster.
func (t *Tools) analyzeClusterMachines(ctx context.Context, toolReq *mcp.CallToolRequest, params inspectClusterMachinesParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, log, err
	}

	log.Debug("Analyzing Cluster Machines")

	machines, machineSets, machineDeployments, err := t.getAllCAPIMachineResources(ctx, log, getCAPIMachineResourcesParams{
		namespace:     params.Namespace,
		targetCluster: params.Cluster,
		kinds: &capiResourceKinds{
			machines:           params.IncludeMachines,
			machineSets:        params.IncludeMachineSets,
			machineDeployments: params.IncludeMachineDeployments,
		},
	})
	if err != nil && !errors.IsNotFound(err) {
		log.Error("failed to lookup CAPI machine resources", zap.Error(err))
		return nil, nil, err
	}

	var resources []*unstructured.Unstructured
	if len(machines) > 0 {
		resources = append(resources, machines...)
	}

	if len(machineSets) > 0 {
		resources = append(resources, machineSets...)
	}

	if len(machineDeployments) > 0 {
		resources = append(resources, machineDeployments...)
	}

	// all CAPI resources exist in the local cluster only.
	mcpResponse, err := response.CreateMcpResponse(resources, LocalCluster)
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
