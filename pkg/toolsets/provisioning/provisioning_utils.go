package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	CAPIMachineDeploymentKind = "MachineDeployment"
	CAPIMachineSetKind        = "MachineSet"
	CAPIMachineKind           = "Machine"

	LocalCluster                     = "local"
	DefaultClusterResourcesNamespace = "fleet-default"
)

type getCAPIMachineResourcesParams struct {
	namespace     string
	targetCluster string
	machineName   string
}

func (t *Tools) getCAPIMachineResourcesByName(ctx context.Context, toolReq *mcp.CallToolRequest, log *zap.Logger, params getCAPIMachineResourcesParams) (*unstructured.Unstructured, *unstructured.Unstructured, *unstructured.Unstructured, error) {
	if params.namespace == "" {
		params.namespace = DefaultClusterResourcesNamespace
	}

	log.Debug("fetching CAPI machine by name",
		zap.String("namespace", params.namespace),
		zap.String("machineName", params.machineName))

	capiMachine, err := t.client.GetResourceAtAnyAPIVersion(ctx, client.GetParams{
		Cluster:   LocalCluster,
		Kind:      converter.CAPIMachineResourceKind,
		Namespace: params.namespace,
		Name:      params.machineName,
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Debug("CAPI machine not found",
				zap.String("namespace", params.namespace),
				zap.String("machineName", params.machineName))
			return nil, nil, nil, apierrors.NewNotFound(schema.GroupResource{
				Group:    converter.CAPIGroup,
				Resource: CAPIMachineKind,
			}, params.machineName)
		}
		log.Error("failed to get CAPI machine",
			zap.String("namespace", params.namespace),
			zap.String("machineName", params.machineName),
			zap.Error(err))
		return nil, nil, nil, fmt.Errorf("failed to get machine: %w", err)
	}
	log.Debug("found CAPI machine",
		zap.String("namespace", params.namespace),
		zap.String("machine", params.machineName))

	var capiMachineSet, capiMachineDeployment *unstructured.Unstructured
	foundSetOwner := false
	for _, ownerRef := range capiMachine.GetOwnerReferences() {
		if ownerRef.Kind != CAPIMachineSetKind {
			continue
		}
		foundSetOwner = true
		log.Debug("fetching CAPI machine set from owner reference",
			zap.String("namespace", params.namespace),
			zap.String("machineSet", ownerRef.Name))
		capiMachineSet, err = t.client.GetResourceAtAnyAPIVersion(ctx, client.GetParams{
			Cluster:   LocalCluster,
			Kind:      converter.CAPIMachineSetResourceKind,
			Namespace: params.namespace,
			Name:      ownerRef.Name,
			Token:     middleware.Token(ctx),
		})
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Debug("CAPI machine set not found",
					zap.String("namespace", params.namespace),
					zap.String("machineSet", ownerRef.Name))
				return capiMachine, nil, nil, nil
			}
			log.Error("failed to get CAPI machine set",
				zap.String("namespace", params.namespace),
				zap.String("machineSet", ownerRef.Name),
				zap.Error(err))
			return nil, nil, nil, fmt.Errorf("failed to get machine set: %w", err)
		}
	}
	if !foundSetOwner || capiMachineSet == nil {
		log.Debug("CAPI machine has no machine set owner",
			zap.String("namespace", params.namespace),
			zap.String("machine", params.machineName))
		return capiMachine, nil, nil, nil
	}
	log.Debug("found CAPI machine set",
		zap.String("namespace", params.namespace),
		zap.String("machine", params.machineName),
		zap.String("machineSet", capiMachineSet.GetName()))

	foundDeploymentOwner := false
	for _, ownerRef := range capiMachineSet.GetOwnerReferences() {
		if ownerRef.Kind != CAPIMachineDeploymentKind {
			continue
		}
		foundDeploymentOwner = true
		log.Debug("fetching CAPI machine deployment from owner reference",
			zap.String("namespace", params.namespace),
			zap.String("machineDeployment", ownerRef.Name))
		capiMachineDeployment, err = t.client.GetResourceAtAnyAPIVersion(ctx, client.GetParams{
			Cluster:   LocalCluster,
			Kind:      converter.CAPIMachineDeploymentResourceKind,
			Namespace: params.namespace,
			Name:      ownerRef.Name,
			Token:     middleware.Token(ctx),
		})
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Debug("CAPI machine deployment not found",
					zap.String("namespace", params.namespace),
					zap.String("machineDeployment", ownerRef.Name))
				return capiMachine, capiMachineSet, nil, nil
			}
			log.Error("failed to get CAPI machine deployment",
				zap.String("namespace", params.namespace),
				zap.String("machineDeployment", ownerRef.Name),
				zap.Error(err))
			return nil, nil, nil, fmt.Errorf("failed to get machine deployment: %w", err)
		}
	}
	if !foundDeploymentOwner {
		log.Debug("CAPI machine set has no machine deployment owner",
			zap.String("namespace", params.namespace),
			zap.String("machineSet", capiMachineSet.GetName()))
		return capiMachine, capiMachineSet, nil, nil
	}

	if capiMachineDeployment != nil {
		log.Debug("found CAPI machine deployment",
			zap.String("namespace", params.namespace),
			zap.String("machine", params.machineName),
			zap.String("machineDeployment", capiMachineDeployment.GetName()))
	}

	return capiMachine, capiMachineSet, capiMachineDeployment, nil
}

// getAllCAPIMachineResources retrieves the cluster API machines, machine sets, and machine deployments for a given provisioning cluster.
func (t *Tools) getAllCAPIMachineResources(ctx context.Context, toolReq *mcp.CallToolRequest, log *zap.Logger, params getCAPIMachineResourcesParams) ([]*unstructured.Unstructured, []*unstructured.Unstructured, []*unstructured.Unstructured, error) {
	if params.namespace == "" {
		params.namespace = DefaultClusterResourcesNamespace
	}

	log.Debug("fetching all CAPI machine resources",
		zap.String("namespace", params.namespace),
		zap.String("targetCluster", params.targetCluster))

	clusterSelector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{
			"cluster.x-k8s.io/cluster-name": params.targetCluster,
		},
	})
	if err != nil {
		log.Error("failed to create label selector for cluster machines",
			zap.String("targetCluster", params.targetCluster),
			zap.Error(err))
		return nil, nil, nil, fmt.Errorf("failed to create machine selector for cluster machines")
	}

	var capiMachines, capiMachineSets, capiMachineDeployments []*unstructured.Unstructured

	log.Debug("listing CAPI machine deployments",
		zap.String("namespace", params.namespace),
		zap.String("targetCluster", params.targetCluster))
	deployments, err := t.client.GetResourcesAtAnyAPIVersion(ctx, client.ListParams{
		Cluster:       LocalCluster,
		Kind:          converter.CAPIMachineDeploymentResourceKind,
		Namespace:     params.namespace,
		LabelSelector: clusterSelector.String(),
		Token:         middleware.Token(ctx),
	})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error("failed to list CAPI machine deployments",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster),
			zap.Error(err))
		return nil, nil, nil, fmt.Errorf("failed to list machine deployments: %w", err)
	}
	if err == nil {
		capiMachineDeployments = deployments
		log.Debug("found CAPI machine deployments",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster),
			zap.Int("count", len(capiMachineDeployments)))
	} else {
		log.Debug("no CAPI machine deployments found",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster))
	}

	log.Debug("listing CAPI machine sets",
		zap.String("namespace", params.namespace),
		zap.String("targetCluster", params.targetCluster))
	machineSets, err := t.client.GetResourcesAtAnyAPIVersion(ctx, client.ListParams{
		Cluster:       LocalCluster,
		Kind:          converter.CAPIMachineSetResourceKind,
		Namespace:     params.namespace,
		LabelSelector: clusterSelector.String(),
		Token:         middleware.Token(ctx),
	})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error("failed to list CAPI machine sets",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster),
			zap.Error(err))
		return nil, nil, nil, fmt.Errorf("failed to list machine sets: %w", err)
	}
	if err == nil {
		capiMachineSets = machineSets
		log.Debug("found CAPI machine sets",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster),
			zap.Int("count", len(capiMachineSets)))
	} else {
		log.Debug("no CAPI machine sets found",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster))
	}

	log.Debug("listing CAPI machines",
		zap.String("namespace", params.namespace),
		zap.String("targetCluster", params.targetCluster))
	machines, err := t.client.GetResourcesAtAnyAPIVersion(ctx, client.ListParams{
		Cluster:       LocalCluster,
		Kind:          converter.CAPIMachineResourceKind,
		Namespace:     params.namespace,
		LabelSelector: clusterSelector.String(),
		Token:         middleware.Token(ctx),
	})
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error("failed to list CAPI machines",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster),
			zap.Error(err))
		return nil, nil, nil, fmt.Errorf("failed to list machines: %w", err)
	}
	if err == nil {
		capiMachines = machines
		log.Debug("found CAPI machines",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster),
			zap.Int("count", len(capiMachines)))
	} else {
		log.Debug("no CAPI machines found",
			zap.String("namespace", params.namespace),
			zap.String("targetCluster", params.targetCluster))
	}

	return capiMachines, capiMachineSets, capiMachineDeployments, nil
}

func (t *Tools) getProvisioningCluster(ctx context.Context, toolReq *mcp.CallToolRequest, log *zap.Logger, ns, clusterName string) (*unstructured.Unstructured, provisioningV1.Cluster, error) {
	log.Debug("fetching provisioning cluster",
		zap.String("namespace", ns),
		zap.String("cluster", clusterName))

	provisioningClusterResource, err := t.client.GetResource(ctx, client.GetParams{
		Cluster:   LocalCluster,
		Kind:      converter.ProvisioningClusterResourceKind,
		Namespace: ns,
		Name:      clusterName,
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Debug("provisioning cluster not found",
				zap.String("namespace", ns),
				zap.String("cluster", clusterName))
			return nil, provisioningV1.Cluster{}, apierrors.NewNotFound(schema.GroupResource{
				Group:    converter.ProvisioningGroup,
				Resource: "cluster",
			}, clusterName)
		}
		log.Error("failed to get provisioning cluster",
			zap.String("namespace", ns),
			zap.String("cluster", clusterName),
			zap.Error(err))
		return nil, provisioningV1.Cluster{}, err
	}

	log.Debug("converting unstructured provisioning cluster to typed object",
		zap.String("cluster", clusterName))
	provCluster := provisioningV1.Cluster{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(provisioningClusterResource.Object, &provCluster)
	if err != nil {
		log.Error("failed to convert provisioning cluster from unstructured",
			zap.String("namespace", ns),
			zap.String("cluster", clusterName),
			zap.Error(err))
		return nil, provCluster, err
	}

	log.Debug("successfully retrieved provisioning cluster",
		zap.String("namespace", ns),
		zap.String("cluster", clusterName))
	return provisioningClusterResource, provCluster, nil
}

func (t *Tools) getMachinePoolConfigs(ctx context.Context, toolReq *mcp.CallToolRequest, log *zap.Logger, provCluster provisioningV1.Cluster) ([]*unstructured.Unstructured, error) {
	log.Debug("fetching machine pool configs",
		zap.String("cluster", provCluster.Name))

	if provCluster.Spec.RKEConfig == nil || provCluster.Spec.RKEConfig.MachinePools == nil || len(provCluster.Spec.RKEConfig.MachinePools) == 0 {
		log.Debug("no machine pools found in cluster RKE config",
			zap.String("cluster", provCluster.Name))
		return nil, apierrors.NewNotFound(schema.GroupResource{
			Group:    "rke-machine-config.cattle.io",
			Resource: "",
		}, provCluster.Name)
	}

	var resources []*unstructured.Unstructured
	pools := provCluster.Spec.RKEConfig.MachinePools
	log.Debug("processing machine pools",
		zap.String("cluster", provCluster.Name),
		zap.Int("poolCount", len(pools)))

	for _, pool := range pools {
		poolName := pool.Name
		configName := pool.NodeConfig.Name
		configKind := pool.NodeConfig.GroupVersionKind().Kind
		resourceName := fmt.Sprintf("%ss", strings.ToLower(configKind))

		log.Debug("fetching machine config for pool",
			zap.String("cluster", provCluster.Name),
			zap.String("pool", poolName),
			zap.String("configName", configName),
			zap.String("configKind", configKind))

		config, err := t.client.GetResourceByGVR(ctx, client.GetParams{
			Cluster:   LocalCluster,
			Namespace: DefaultClusterResourcesNamespace,
			Name:      configName,
			Token:     middleware.Token(ctx),
		}, schema.GroupVersionResource{
			Group:    "rke-machine-config.cattle.io",
			Version:  "v1",
			Resource: resourceName,
		})
		if apierrors.IsNotFound(err) {
			log.Debug("machine config not found for pool, skipping",
				zap.String("cluster", provCluster.Name),
				zap.String("pool", poolName),
				zap.String("configName", configName))
			continue
		}
		if err != nil {
			log.Error("failed to get machine config from pool",
				zap.String("cluster", provCluster.Name),
				zap.String("pool", poolName),
				zap.String("configName", configName),
				zap.Error(err))
			return nil, err
		}
		log.Debug("successfully retrieved machine config for pool",
			zap.String("cluster", provCluster.Name),
			zap.String("pool", poolName),
			zap.String("configName", configName))
		resources = append(resources, config)
	}

	log.Debug("successfully retrieved machine pool configs",
		zap.String("cluster", provCluster.Name),
		zap.Int("configCount", len(resources)))
	return resources, nil
}

func supportedKubernetesVersion(url, distro, version string, log *zap.Logger) (string, []string, bool, error) {
	versions, err := getKDMReleases(url, distro)
	if err != nil {
		return "", nil, false, err
	}

	distroVersion := fmt.Sprintf("%s+%s", version, distro)
	log.Debug("Looking for Kubernetes version", zap.String("version", distroVersion))
	var potentialVersions []string
	for _, ver := range versions {
		// If the user has explicitly requested a complete version (v1.33.3+rke2r1, v1.33.3+k3s1, etc.) and it is available, use it.
		// otherwise we need to find the latest release for that version.
		if ver == version {
			return version, versions, true, nil
		}
		if strings.Contains(ver, distroVersion) {
			log.Debug("found a potential match for the requested Kubernetes version and distro", zap.String("potentialVersion", ver))
			potentialVersions = append(potentialVersions, ver)
		}
	}

	if len(potentialVersions) == 0 {
		log.Debug("no potential versions found matching the requested Kubernetes version and distro", zap.String("version", version), zap.String("distro", distro))
		return "", versions, false, nil
	}

	latestVersionString := ""
	latestVersion := 0
	split := "rke2r"
	if distro == "k3s" {
		split = "k3s"
	}

	log.Debug("looking for the latest Kubernetes version matching the requested version and distro", zap.String("version", version), zap.String("distro", distro))
	for _, potentialVersion := range potentialVersions {
		_, releaseVersion, found := strings.Cut(potentialVersion, split)
		if !found {
			log.Debug("failed to parse potential version, skipping", zap.String("potentialVersion", potentialVersion))
			continue
		}
		verInt, err := strconv.Atoi(releaseVersion)
		if err != nil {
			log.Debug("failed to convert release version to integer, skipping", zap.String("releaseVersion", releaseVersion), zap.Error(err))
			continue
		}
		if verInt > latestVersion {
			log.Debug("found a new latest version", zap.String("potentialVersion", potentialVersion), zap.Int("releaseVersion", verInt))
			latestVersion = verInt
			latestVersionString = potentialVersion
		}
	}

	if latestVersionString == "" {
		log.Debug("no latest version found, unsupported kubernetes version", zap.String("version", version))
		return "", versions, false, nil
	}

	log.Debug("successfully found Kubernetes version", zap.String("version", latestVersionString))
	return latestVersionString, versions, true, nil
}

// getKDMReleases fetches the list of available Kubernetes versions for the specified Rancher server
func getKDMReleases(url, distro string) ([]string, error) {
	if distro != "rke2" && distro != "k3s" {
		return nil, fmt.Errorf("invalid distro: %s. Valid values are 'rke2' and 'k3s'", distro)
	}

	resp, err := http.DefaultClient.Get(fmt.Sprintf("%s/v1-%s-release/releases", url, distro))
	if err != nil {
		return nil, fmt.Errorf("failed to get KDM releases: %w", err)
	}

	kdm := make(map[string]interface{})
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read KDM response body: %w", err)
	}

	resp.Body.Close()
	err = json.Unmarshal(b, &kdm)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal KDM response: %w", err)
	}

	allReleases, ok := kdm["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid KDM response format: missing 'data' field")
	}

	var versions []string
	for _, release := range allReleases {
		currentRelease, ok := release.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid KDM response format: release is not an object")
		}
		currentVersion, ok := currentRelease["version"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid KDM response format: release version is not a string")
		}
		versions = append(versions, currentVersion)
	}

	return versions, nil
}

func supportedCNI(cni string) ([]string, bool) {
	// NB: These are also hard-coded in the Rancher dashboard codebase.
	supportedCNIS := []string{
		"calico",
		"canal",
		"cilium",
		"flannel",
		"multus,canal",
		"multus,cilium",
		"multus,calico",
		"none",
	}
	if !slices.Contains(supportedCNIS, cni) {
		return supportedCNIS, false
	}
	return supportedCNIS, true
}

func makeRancherRequest(ctx context.Context, rancherURL, method, path, token string, requestBody []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("%s/%s", rancherURL, strings.TrimPrefix(path, "/")), bytes.NewReader(requestBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if err != io.EOF {
			return nil, resp.StatusCode, fmt.Errorf("failed to read response body from Rancher API after cluster creation: %w", err)
		}
	}

	return body, resp.StatusCode, nil
}
