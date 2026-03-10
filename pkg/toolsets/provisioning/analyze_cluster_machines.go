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

type inspectClusterMachinesParams struct {
	Cluster   string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace where the resource is located. The default namespace will be used if not provided"`
}

// analyzeClusterMachines returns the cluster API machines, machine sets, and machine deployments, for a given provisioning cluster.
func (t *Tools) analyzeClusterMachines(ctx context.Context, toolReq *mcp.CallToolRequest, params inspectClusterMachinesParams) (*mcp.CallToolResult, any, error) {
	ns := params.Namespace
	if ns == "" {
		ns = "fleet-default"
	}

	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":   params.Cluster,
		"namespace": params.Namespace,
	})
	log.Info("Analyzing Cluster Machines")

	machines, machineSets, machineDeployments, err := t.getAllCAPIMachineResources(ctx, toolReq, log, getCAPIMachineResourcesParams{
		namespace:     ns,
		targetCluster: params.Cluster,
	})
	if err != nil && !errors.IsNotFound(err) {
		log.Error("failed to lookup CAPI machine resources", zap.Error(err))
		return nil, nil, err
	}

	var resources []*unstructured.Unstructured
	if machines != nil && len(machines) > 0 {
		resources = append(resources, machines...)
	}

	if machineSets != nil && len(machineSets) > 0 {
		resources = append(resources, machineSets...)
	}

	if machineDeployments != nil && len(machineDeployments) > 0 {
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
