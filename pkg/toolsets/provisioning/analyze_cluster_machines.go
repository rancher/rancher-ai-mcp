package provisioning

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
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
	IncludeMachines           bool   `json:"includeMachines" jsonschema:"set to true to return the cluster's individual CAPI Machines, which carry per-node phase, conditions, and failure details. If the cluster has no CAPI Machines (e.g. imported and hosted clusters), the Rancher management node objects are returned instead, trimmed to their names and conditions"`
	FullNodes                 bool   `json:"fullNodes,omitempty" jsonschema:"set to true to return the complete Rancher management node objects, instead of only their names and conditions, when the cluster has no CAPI Machines"`
	JSONPath                  string `json:"jsonPath,omitempty" jsonschema:"optional JSONPath filter predicate to select which of the returned resources to keep. Use @ to reference a resource, e.g. @.status.nodeName==\"worker-1\" or @.spec.worker==true. It is evaluated against the complete resources, before any trimming. Only resources matching the predicate are returned"`
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

	// clusters which are not provisioned by Rancher, such as imported and hosted clusters, have no CAPI machines. Fall back
	// to the management node objects, which exist for every cluster, so that their nodes can still be identified.
	// This is analogous to the Rancher agent node listing tool, but specific for the provisioning agent.
	var nodes []*unstructured.Unstructured
	var responseOptions []response.Option
	if params.IncludeMachines && len(machines) == 0 {
		token := middleware.Token(ctx)
		clusterID, err := t.client.GetClusterID(ctx, token, params.Cluster)
		if err != nil {
			err = fmt.Errorf("failed to resolve cluster %s: %w", params.Cluster, err)
			log.Error("failed to lookup management nodes", zap.Error(err))
			return nil, nil, err
		}

		// Management nodes live in a namespace named after their cluster ID.
		nodes, err = t.client.GetResources(ctx, client.ListParams{
			Cluster:   LocalCluster,
			Kind:      converter.ManagementNodeResourceKind,
			Namespace: clusterID,
			Token:     token,
		})
		if err != nil {
			err = fmt.Errorf("failed to list nodes of cluster %s: %w", params.Cluster, err)
			log.Error("failed to lookup management nodes", zap.Error(err))
			return nil, nil, err
		}

		if len(nodes) > 0 {
			log.Debug("no CAPI machines found, returning management nodes", zap.Int("nodes", len(nodes)))

			note := fmt.Sprintf("Cluster %s has no CAPI machines, so its Rancher management node objects are returned instead. status.nodeName is the name of the Kubernetes node.", params.Cluster)
			if !params.FullNodes {
				note += " Only the names and conditions of the nodes are included, set fullNodes to true for the complete objects."
			}

			responseOptions = append(responseOptions, response.WithNotes(note))
		}
	}

	var resources []*unstructured.Unstructured
	if len(machines) > 0 {
		resources = append(resources, machines...)
	}

	if len(nodes) > 0 {
		resources = append(resources, nodes...)
	}

	if len(machineSets) > 0 {
		resources = append(resources, machineSets...)
	}

	if len(machineDeployments) > 0 {
		resources = append(resources, machineDeployments...)
	}

	// the filter is evaluated on the complete resources so that it can use any field, trimming comes after.
	if params.JSONPath != "" {
		resources, err = utils.FilterByJSONPath(resources, params.JSONPath)
		if err != nil {
			log.Error("failed to filter resources by jsonpath", zap.Error(err))
			return nil, nil, err
		}
	}

	if !params.FullNodes {
		for i, resource := range resources {
			if isManagementNode(resource) {
				resources[i] = trimManagementNode(resource)
			}
		}
	}

	// all CAPI resources exist in the local cluster only.
	mcpResponse, err := response.CreateMcpResponse(resources, LocalCluster, responseOptions...)
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
