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

type getClusterMachineParams struct {
	Cluster                  string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	MachineName              string `json:"machineName" jsonschema:"the name of the machine to get"`
	IncludeMachineDeployment bool   `json:"includeMachineDeployment" jsonschema:"set to true to return the MachineDeployment that owns this machine, which summarizes desired and ready replicas for its machine pool"`
	IncludeMachineSet        bool   `json:"includeMachineSet" jsonschema:"set to true to return the MachineSet that owns this machine, which sits between the MachineDeployment and the Machine and reflects rollout state"`
	IncludeMachine           bool   `json:"includeMachine" jsonschema:"set to true to return the CAPI Machine itself, which carries the node's phase, conditions, and failure details"`
}

func (params *getClusterMachineParams) validate(toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":     params.Cluster,
		"machineName": params.MachineName,
	})
	if err := validateClusterName(params.Cluster); err != nil {
		return nil, err
	}
	if !params.IncludeMachineDeployment && !params.IncludeMachineSet && !params.IncludeMachine {
		return nil, fmt.Errorf("at least one of includeMachineDeployment, includeMachineSet, or includeMachine must be true")
	}
	return log, nil
}

// getClusterMachine returns the selected CAPI resources (the machine, its MachineSet, and its MachineDeployment) for a given provisioning cluster and machine name.
func (t *Tools) getClusterMachine(ctx context.Context, toolReq *mcp.CallToolRequest, params getClusterMachineParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Info("Getting Cluster Machine")

	var resources []*unstructured.Unstructured
	machine, machineSet, machineDeployment, err := t.getCAPIMachineResourcesByName(ctx, log, getCAPIMachineResourcesParams{
		namespace:     "fleet-default",
		targetCluster: params.Cluster,
		machineName:   params.MachineName,
		kinds: &capiResourceKinds{
			machines:           params.IncludeMachine,
			machineSets:        params.IncludeMachineSet,
			machineDeployments: params.IncludeMachineDeployment,
		},
	})
	if err != nil && !errors.IsNotFound(err) {
		log.Error("failed to lookup capi machine", zap.Error(err))
		return nil, nil, err
	}

	if machine != nil {
		resources = append(resources, machine)
	}
	if machineSet != nil {
		resources = append(resources, machineSet)
	}
	if machineDeployment != nil {
		resources = append(resources, machineDeployment)
	}

	// all CAPI resources exist in the local cluster only.
	mcpResponse, err := response.CreateMcpResponse(resources, "local")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
