package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"mcp/internal/middleware"
	"mcp/pkg/converter"
	"mcp/pkg/response"
	"mcp/pkg/utils"
	"strings"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type CreateCustomClusterParams struct {
	ClusterName        string `json:"clusterName" jsonschema:"the name of the provisioning cluster"`
	ClusterDescription string `json:"clusterDescription" jsonschema:"the description of the provisioning cluster"`
	CNI                string `json:"CNI" jsonschema:"the name of the CNI (Container Networking Interface) to use"`
	KubernetesVersion  string `json:"kubernetesVersion" jsonschema:"the Kubernetes version of the cluster"`
	Distribution       string `json:"distribution" jsonschema:"the distribution of the provisioning cluster (rke2 or k3s)"`
}

func (t *Tools) CreateCustomCluster(ctx context.Context, toolReq *mcp.CallToolRequest, params CreateCustomClusterParams) (*mcp.CallToolResult, any, error) {
	log := utils.NewChildLogger(toolReq, map[string]string{
		"clusterName":        params.ClusterName,
		"clusterDescription": params.ClusterDescription,
		"CNI":                params.CNI,
		"KubernetesVersion":  params.KubernetesVersion,
		"Distribution":       params.Distribution,
	})

	log.Debug("creating a custom cluster")

	if params.ClusterName == "" {
		log.Debug("cluster name is required")
		return nil, nil, fmt.Errorf("ClusterName is required")
	}

	if params.Distribution != "rke2" && params.Distribution != "k3s" {
		log.Debug("invalid distribution", zap.String("distribution", params.Distribution))
		return nil, nil, fmt.Errorf("invalid value for Distribution: %s. Valid values are 'rke2' and 'k3s'", params.Distribution)
	}

	allCNIs, cniSupported := supportedCNI(params.CNI)
	if !cniSupported {
		log.Debug("invalid CNI", zap.String("CNI", params.CNI))
		return nil, nil, fmt.Errorf("unsupported CNI \"%s\". Valid values are \"%v\"", params.CNI, strings.Join(allCNIs, "\", \""))
	}

	fullVersion, allSupportedVersions, supported, err := supportedKubernetesVersion(toolReq.Extra.Header.Get(urlHeader), params.Distribution, params.KubernetesVersion, log)
	if err != nil {
		log.Error("error getting supported Kubernetes version", zap.Error(err))
		return nil, nil, fmt.Errorf("error checking supported Kubernetes versions: %w", err)
	}

	if !supported {
		log.Error("unsupported distribution", zap.String("distribution", params.Distribution))
		return nil, nil, fmt.Errorf("unsupported Kubernetes version: %s for distribution: %s. Only support versions %v", params.KubernetesVersion, params.Distribution, allSupportedVersions)
	}

	custom := provisioningV1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: "provisioning.cattle.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.ClusterName,
			Namespace: DefaultClusterResourcesNamespace,
			Annotations: map[string]string{
				"field.cattle.io/description": params.ClusterDescription,
			},
		},
		Spec: provisioningV1.ClusterSpec{
			KubernetesVersion: fullVersion,
			RKEConfig: &provisioningV1.RKEConfig{
				RKEClusterSpecCommon: v1.RKEClusterSpecCommon{
					ETCD: &v1.ETCD{
						SnapshotRetention:    5,
						SnapshotScheduleCron: "0 */5 * * *",
					},
					MachineGlobalConfig: v1.GenericMap{
						Data: map[string]interface{}{
							"cni": params.CNI,
						},
					},
					UpgradeStrategy: v1.ClusterUpgradeStrategy{
						ControlPlaneConcurrency: "1",
						ControlPlaneDrainOptions: v1.DrainOptions{
							DeleteEmptyDirData: true,
							GracePeriod:        -1,
							IgnoreDaemonSets:   toPtr(true),
							Timeout:            120,
						},
						WorkerConcurrency: "1",
						WorkerDrainOptions: v1.DrainOptions{
							DeleteEmptyDirData: true,
							GracePeriod:        -1,
							IgnoreDaemonSets:   toPtr(true),
							Timeout:            120,
						},
					},
				},
			},
		},
	}

	objBytes, err := json.Marshal(custom)
	if err != nil {
		log.Error("failed to marshal resource", zap.String("tool", "createKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal resource: %w", err)
	}

	unstructuredObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(objBytes, unstructuredObj); err != nil {
		log.Error("failed to create unstructured resource", zap.String("tool", "createKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create unstructured object: %w", err)
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), toolReq.Extra.Header.Get(urlHeader), DefaultClusterResourcesNamespace, LocalCluster, converter.K8sKindsToGVRs[converter.ProvisioningClusterResourceKind])
	if err != nil {
		return nil, nil, fmt.Errorf("error getting resource interface: %w", err)
	}

	createdCluster, err := resourceInterface.Create(ctx, unstructuredObj, metav1.CreateOptions{})
	if err != nil {
		log.Error("failed to create resource", zap.String("tool", "createKubernetesResource"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create resource %s: %w", params.ClusterName, err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{createdCluster}, LocalCluster)
	if err != nil {
		log.Error(fmt.Sprintf("failed to create mcp response: %v", err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
