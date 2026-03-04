package provisioning

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type getClusterMachineParams struct {
	Cluster     string `json:"cluster" jsonschema:"the name of the cluster the machines belong to"`
	MachineName string `json:"machineName" jsonschema:"the name of the machine to retrieve"`
}

// getClusterMachine returns the cluster API machine for a given provisioning cluster and machine name.
func (t *Tools) getClusterMachine(ctx context.Context, toolReq *mcp.CallToolRequest, params getClusterMachineParams) (*mcp.CallToolResult, any, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":     params.Cluster,
		"machineName": params.MachineName,
	})

	log.Info("Getting Cluster Machine")

	var resources []*unstructured.Unstructured
	machine, machineSet, machineDeployment, err := t.getCAPIMachineResourcesByName(ctx, toolReq, log, getCAPIMachineResourcesParams{
		namespace:     "fleet-default",
		targetCluster: params.Cluster,
		machineName:   params.MachineName,
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
