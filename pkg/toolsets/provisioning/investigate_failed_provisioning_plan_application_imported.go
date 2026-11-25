package provisioning

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	importedMachineProvider = "imported"
	// importedDay2OpsFeature is the Rancher feature which gates day two operations on all imported clusters. There is no per cluster override.
	importedDay2OpsFeature = "imported-day-2-ops"
	// operationsEnabledAnnotation is set on the management cluster to opt a single imported cluster in or out of day two operations.
	operationsEnabledAnnotation = "rancher.io/operations-enabled"
	// importedDay2OpsSetting is the default for imported clusters which do not have the annotation.
	importedDay2OpsSetting = "imported-cluster-day2-ops-enabled"

	importedPlanClusterLabel = "rke.cattle.io/cluster-name"
	importedPlanNodeLabel    = "rke.cattle.io/node-name"
	machinePlanSecretType    = "rke.cattle.io/machine-plan"
)

// planTarget identifies the downstream node a plan applies to and where its machine plan is stored.
type planTarget struct {
	nodeName            string
	planSecretNamespace string
	planSecretName      string
	imported            bool
}

// resolveImportedPlanTarget finds the node and machine plan for a node of an imported cluster. Imported clusters have
// no CAPI machines, and only run the system-agent if day two operations are enabled for them.
func (t *Tools) resolveImportedPlanTarget(ctx context.Context, log *zap.Logger, cluster, nodeName string) (planTarget, error) {
	token := middleware.Token(ctx)
	clusterID, err := t.client.GetClusterID(ctx, token, cluster)
	if err != nil {
		return planTarget{}, fmt.Errorf("failed to resolve cluster %s: %w", cluster, err)
	}

	managementCluster, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: LocalCluster,
		Kind:    converter.ManagementClusterResourceKind,
		Name:    clusterID,
		Token:   token,
	})
	if err != nil {
		return planTarget{}, fmt.Errorf("failed to get management cluster %s: %w", clusterID, err)
	}

	// anything which is not an imported cluster and has no CAPI machines is a hosted cluster (aks, eks, gke).
	machineProvider, _, err := unstructured.NestedString(managementCluster.Object, "status", "info", "machineProvider")
	if err != nil {
		return planTarget{}, fmt.Errorf("failed to read the machine provider of cluster %s: %w", cluster, err)
	}

	if machineProvider == "aks" || machineProvider == "eks" || machineProvider == "gke" {
		return planTarget{}, fmt.Errorf("cluster %s has no CAPI machine resources and its machine provider is %q. This tool only supports clusters managed by Rancher machine plans and imported clusters, and cannot be run against hosted clusters", cluster, machineProvider)
	}
	if machineProvider != importedMachineProvider {
		return planTarget{}, fmt.Errorf("cluster %s has no CAPI machine resources and its machine provider is %q. This tool supports Rancher machine-plan clusters and imported clusters with day two operations enabled", cluster, machineProvider)
	}

	if strings.TrimSpace(nodeName) == "" {
		return planTarget{}, fmt.Errorf("Kubernetes node name is required for imported clusters")
	}

	// check if the feature which deploys the system-agent onto imported clusters is enabled
	if err := checkImportedDay2Ops(ctx, log, t.client, managementCluster); err != nil {
		return planTarget{}, err
	}

	return planTarget{
		nodeName:            nodeName,
		planSecretNamespace: clusterID,
		imported:            true,
	}, nil
}

// checkImportedDay2Ops returns an error explaining why the system-agent is not expected to be running on the imported
// cluster, or nil if day two operations are enabled for it. In order of precedence:
//  1. The imported-day-2-ops feature gates everything. While it is disabled, the annotation and setting have no effect.
//  2. The setting must exist. It should always, but without it nothing is allowed, not even an annotation set to true.
//  3. The operations-enabled annotation on the cluster, set to true or false, always wins over the setting's value.
//  4. The setting's value is the default for clusters which do not have the annotation.
func checkImportedDay2Ops(ctx context.Context, log *zap.Logger, c utils.ResourceGetter, managementCluster *unstructured.Unstructured) error {
	clusterID := managementCluster.GetName()

	featureEnabled, err := utils.IsFeatureEnabled(ctx, c, importedDay2OpsFeature)
	if err != nil {
		return fmt.Errorf("failed to check whether day two operations are available on imported cluster %s: %w", clusterID, err)
	}

	if !featureEnabled {
		return fmt.Errorf("the %s feature is not enabled in Rancher, so the system-agent is not expected to be running on imported cluster %s. The feature must be enabled to use this tool, the %s setting and %s annotation have no effect while it is disabled", importedDay2OpsFeature, clusterID, importedDay2OpsSetting, operationsEnabledAnnotation)
	}

	settingValue, settingFound, err := utils.GetSettingValue(ctx, c, importedDay2OpsSetting)
	if err != nil {
		return fmt.Errorf("failed to check the day two operations setting for imported cluster %s: %w", clusterID, err)
	}

	if !settingFound {
		log.Debug("day two operations setting not found", zap.String("setting", importedDay2OpsSetting))
		return fmt.Errorf("the %s setting does not exist, so day two operations cannot be used on imported cluster %s. The %s annotation has no effect without it", importedDay2OpsSetting, clusterID, operationsEnabledAnnotation)
	}

	// Rancher compares these values exactly, so a value such as "True" or " true " is not true to it either. Any
	// annotation other than true or false defers to the setting.
	annotation := managementCluster.GetAnnotations()[operationsEnabledAnnotation]
	if annotation == "true" {
		return nil
	}

	if annotation == "false" {
		return fmt.Errorf("day two operations are disabled for imported cluster %s by its %s annotation, so the system-agent is not expected to be running on its nodes", clusterID, operationsEnabledAnnotation)
	}

	if !utils.IsTrue(settingValue) {
		return fmt.Errorf("day two operations are not enabled for imported cluster %s, so the system-agent is not expected to be running on its nodes. Set the %s annotation to true on the cluster or enable the %s setting to use this tool", clusterID, operationsEnabledAnnotation, importedDay2OpsSetting)
	}

	return nil
}
