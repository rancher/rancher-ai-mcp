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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type InspectClusterParams struct {
	Cluster   string `json:"cluster" jsonschema:"the name of the provisioning cluster"`
	Namespace string `json:"namespace" jsonschema:"the namespace of the resource"`
}

// AnalyzeCluster returns a set of kubernetes resources that can be used to inspect the cluster for debugging and summary purposes.
func (t *Tools) AnalyzeCluster(ctx context.Context, toolReq *mcp.CallToolRequest, params InspectClusterParams) (*mcp.CallToolResult, any, error) {
	ns := params.Namespace
	if ns == "" {
		ns = DefaultClusterResourcesNamespace
		if params.Cluster == LocalCluster {
			ns = "fleet-local"
		}
	}

	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":   params.Cluster,
		"namespace": ns,
	})

	log.Debug("Analyzing cluster")

	provClusterResource, provCluster, err := t.getProvisioningCluster(ctx, toolReq, log, ns, params.Cluster)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error("failed to get provisioning cluster", zap.Error(err))
		return nil, nil, err
	}

	if apierrors.IsNotFound(err) {
		// the only cluster type without a provisioning cluster object is rke1, which is no longer supported.
		log.Warn("provisioning cluster not found, unsupported cluster type")
		return nil, nil, fmt.Errorf("provisioning cluster %s not found in namespace %s", params.Cluster, ns)
	}

	log.Debug("found provisioning cluster",
		zap.String("provisioningCluster", provCluster.Name),
		zap.String("clusterName", provCluster.Status.ClusterName))

	var resources []*unstructured.Unstructured
	resources = append(resources, provClusterResource)

	// get the management cluster, its status may be relevant.
	// NB: Unlike the v1.Cluster object we can't directly import the v3.Cluster
	// since it pulls in a lot of indirect dependencies (operators for aks, eks, gke, etc.)
	log.Debug("fetching management cluster", zap.String("managementCluster", provCluster.Status.ClusterName))
	managementClusterResource, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: LocalCluster,
		Kind:    converter.ManagementClusterResourceKind,
		// Unlike provisioning clusters, management cluster objects are cluster scoped.
		Namespace: "",
		Name:      provCluster.Status.ClusterName,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error("failed to get management cluster",
			zap.String("managementCluster", provCluster.Status.ClusterName),
			zap.Error(err))
		return nil, nil, err
	}
	if apierrors.IsNotFound(err) {
		log.Debug("management cluster not found", zap.String("managementCluster", provCluster.Status.ClusterName))
	}
	if err == nil {
		resources = append(resources, managementClusterResource)
		log.Debug("found management cluster", zap.String("managementCluster", provCluster.Status.ClusterName))
	}

	// get the CAPI cluster
	log.Debug("fetching CAPI cluster", zap.String("capiCluster", provCluster.Name))
	capiClusterResource, err := t.client.GetResourceAtAnyAPIVersion(ctx, client.GetParams{
		Cluster:   LocalCluster,
		Kind:      converter.CAPIClusterResourceKind,
		Namespace: DefaultClusterResourcesNamespace,
		Name:      provCluster.Name,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error("failed to get CAPI cluster",
			zap.String("capiCluster", provCluster.Name),
			zap.Error(err))
		return nil, nil, err
	}
	if apierrors.IsNotFound(err) {
		log.Debug("CAPI cluster not found", zap.String("capiCluster", provCluster.Name))
	}
	if err == nil {
		log.Debug("found CAPI cluster", zap.String("capiCluster", provCluster.Name))
		resources = append(resources, capiClusterResource)
	}

	// get all machine configs for node driver clusters.
	log.Debug("fetching machine pool configs")
	machineConfigs, err := t.getMachinePoolConfigs(ctx, toolReq, log, provCluster)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error("failed to get machine pool configs", zap.Error(err))
		return nil, nil, err
	}
	if apierrors.IsNotFound(err) {
		log.Debug("no machine pool configs found")
	}
	if err == nil && len(machineConfigs) > 0 {
		log.Debug("found machine pool configs", zap.Int("count", len(machineConfigs)))
		resources = append(resources, machineConfigs...)
	}

	// get all the CAPI machine resources
	log.Debug("fetching CAPI machine resources")
	machines, machineSets, machineDeployments, err := t.getAllCAPIMachineResources(ctx, toolReq, log, getCAPIMachineResourcesParams{
		namespace:     DefaultClusterResourcesNamespace,
		targetCluster: params.Cluster,
	})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error("failed to lookup CAPI machines", zap.Error(err))
		return nil, nil, err
	}
	if apierrors.IsNotFound(err) {
		log.Debug("CAPI machine resources not found")
	}
	if err == nil {
		log.Debug("found CAPI machine resources",
			zap.Int("machines", len(machines)),
			zap.Int("machineSets", len(machineSets)),
			zap.Int("machineDeployments", len(machineDeployments)))
	}

	if machines != nil && len(machines) > 0 {
		resources = append(resources, machines...)
	}
	if machineSets != nil && len(machineSets) > 0 {
		resources = append(resources, machineSets...)
	}
	if machineDeployments != nil && len(machineDeployments) > 0 {
		resources = append(resources, machineDeployments...)
	}

	log.Info("cluster analysis complete",
		zap.Int("totalResources", len(resources)))

	mcpResponse, err := response.CreateMcpResponse(resources, LocalCluster)
	if err != nil {
		log.Error("failed to create MCP response",
			zap.Int("resourceCount", len(resources)),
			zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
