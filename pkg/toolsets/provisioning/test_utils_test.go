package provisioning

import (
	"encoding/json"

	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// Test constants
const (
	testURL   = "https://localhost:8080"
	testToken = "fakeToken"
)

// provisioningSchemes returns a runtime scheme with core API types registered
func provisioningSchemes() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = metav1.AddMetaToScheme(scheme)
	_ = provisioningV1.AddToScheme(scheme)
	return scheme
}

// provisioningCustomListKinds returns a map of custom list kinds for CAPI resources
func provisioningCustomListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		{Group: "provisioning.cattle.io", Version: "v1", Resource: "clusters"}:               "ClusterList",
		{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "clusters"}:                "ClusterList",
		{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "machines"}:                "MachineList",
		{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "machinesets"}:             "MachineSetList",
		{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "machinedeployments"}:      "MachineDeploymentList",
		{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}:                 "ClusterList",
		{Group: "rke-machine-config.cattle.io", Version: "v1", Resource: "amazonec2configs"}: "Amazonec2ConfigList",
	}
}

// k3kCustomListKinds returns a map of custom list kinds for K3k resources
func k3kCustomListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		{Group: "k3k.io", Version: "v1beta1", Resource: "clusters"}:          "ClusterList",
		{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}: "ClusterList",
	}
}

// clientsetWithCAPIDiscovery wraps a fake clientset and overrides Discovery to return CAPI groups
type clientsetWithCAPIDiscovery struct {
	*fake.Clientset
}

func (c *clientsetWithCAPIDiscovery) Discovery() discovery.DiscoveryInterface {
	return &fakeProvisioningDiscovery{
		FakeDiscovery: c.Clientset.Discovery().(*fakediscovery.FakeDiscovery),
	}
}

// fakeProvisioningDiscovery extends fake discovery to return CAPI API groups
type fakeProvisioningDiscovery struct {
	*fakediscovery.FakeDiscovery
}

func (d *fakeProvisioningDiscovery) ServerGroups() (*metav1.APIGroupList, error) {
	return &metav1.APIGroupList{
		Groups: []metav1.APIGroup{
			{
				Name: "cluster.x-k8s.io",
				Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: "cluster.x-k8s.io/v1beta1", Version: "v1beta1"},
				},
				PreferredVersion: metav1.GroupVersionForDiscovery{
					GroupVersion: "cluster.x-k8s.io/v1beta1",
					Version:      "v1beta1",
				},
			},
		},
	}, nil
}

// newFakeClientSet creates a fake clientset
func newFakeClientSet() kubernetes.Interface {
	fakeClient := fake.NewClientset()
	return &clientsetWithCAPIDiscovery{
		Clientset: fakeClient,
	}
}

// newCAPIMachine creates a test CAPI Machine object
func newCAPIMachine(name, namespace, clusterName, phase string, machineSetName string) *unstructured.Unstructured {
	machine := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cluster.x-k8s.io/v1beta1",
			"kind":       "Machine",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"cluster.x-k8s.io/cluster-name": clusterName,
				},
			},
			"spec": map[string]interface{}{
				"clusterName": clusterName,
			},
		},
	}

	// Add owner reference if machineSetName is provided
	if machineSetName != "" {
		machine.Object["metadata"].(map[string]interface{})["ownerReferences"] = []interface{}{
			map[string]interface{}{
				"apiVersion": "cluster.x-k8s.io/v1beta1",
				"kind":       "MachineSet",
				"name":       machineSetName,
				"controller": true,
			},
		}
	}

	// Add status if phase is provided
	if phase != "" {
		machine.Object["status"] = map[string]interface{}{
			"phase": phase,
		}
	}

	return machine
}

// newCAPIMachineWithBootstrap creates a test CAPI Machine object with bootstrap config
func newCAPIMachineWithBootstrap(name, namespace, clusterName, phase, machineSetName, bootstrapKind, bootstrapName string) *unstructured.Unstructured {
	machine := newCAPIMachine(name, namespace, clusterName, phase, machineSetName)

	machine.Object["spec"].(map[string]interface{})["bootstrap"] = map[string]interface{}{
		"configRef": map[string]interface{}{
			"kind": bootstrapKind,
			"name": bootstrapName,
		},
	}

	return machine
}

// newCAPIMachineSet creates a test CAPI MachineSet object
func newCAPIMachineSet(name, namespace, clusterName string, replicas, readyReplicas int64, machineDeploymentName string) *unstructured.Unstructured {
	machineSet := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cluster.x-k8s.io/v1beta1",
			"kind":       "MachineSet",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"cluster.x-k8s.io/cluster-name": clusterName,
				},
			},
			"spec": map[string]interface{}{
				"replicas": replicas,
			},
			"status": map[string]interface{}{
				"replicas":      replicas,
				"readyReplicas": readyReplicas,
			},
		},
	}

	// Add owner reference if machineDeploymentName is provided
	if machineDeploymentName != "" {
		machineSet.Object["metadata"].(map[string]interface{})["ownerReferences"] = []interface{}{
			map[string]interface{}{
				"apiVersion": "cluster.x-k8s.io/v1beta1",
				"kind":       "MachineDeployment",
				"name":       machineDeploymentName,
				"controller": true,
			},
		}
	}

	return machineSet
}

// newCAPIMachineDeployment creates a test CAPI MachineDeployment object
func newCAPIMachineDeployment(name, namespace, clusterName string, replicas, readyReplicas int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cluster.x-k8s.io/v1beta1",
			"kind":       "MachineDeployment",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"cluster.x-k8s.io/cluster-name": clusterName,
				},
			},
			"spec": map[string]interface{}{
				"replicas": replicas,
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"cluster.x-k8s.io/cluster-name": clusterName,
					},
				},
			},
			"status": map[string]interface{}{
				"replicas":      replicas,
				"readyReplicas": readyReplicas,
			},
		},
	}
}

// newCAPICluster creates a test CAPI Cluster object
func newCAPICluster(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cluster.x-k8s.io/v1beta1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"controlPlaneEndpoint": map[string]interface{}{
					"host": "localhost",
					"port": int64(6443),
				},
				"controlPlaneRef": map[string]interface{}{
					"apiVersion": "rke.cattle.io/v1",
					"kind":       "RKEControlPlane",
					"name":       name,
					"namespace":  namespace,
				},
			},
			"status": map[string]interface{}{
				"phase": "Provisioned",
			},
		},
	}
}

// newProvisioningCluster creates a test Provisioning Cluster object
func newProvisioningCluster(name, namespace, managementClusterName string) *unstructured.Unstructured {
	cluster := &provisioningV1.Cluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "provisioning.cattle.io/v1",
			Kind:       "Cluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: provisioningV1.ClusterSpec{},
		Status: provisioningV1.ClusterStatus{
			ClusterName: managementClusterName,
			Ready:       true,
		},
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cluster)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: unstructuredObj}
}

// newProvisioningClusterWithRKEConfig creates a test Provisioning Cluster object with RKE config
func newProvisioningClusterWithRKEConfig(name, namespace, managementClusterName string, machinePools []provisioningV1.RKEMachinePool) *unstructured.Unstructured {
	cluster := &provisioningV1.Cluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "provisioning.cattle.io/v1",
			Kind:       "Cluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: provisioningV1.ClusterSpec{
			RKEConfig: &provisioningV1.RKEConfig{
				MachinePools: machinePools,
			},
		},
		Status: provisioningV1.ClusterStatus{
			ClusterName: managementClusterName,
			Ready:       true,
		},
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cluster)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: unstructuredObj}
}

// newManagementCluster creates a test Management Cluster object
func newManagementCluster(name string, ready bool) *unstructured.Unstructured {
	conditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": "False",
		},
	}
	if ready {
		conditions = []interface{}{
			map[string]interface{}{
				"type":   "Ready",
				"status": "True",
			},
		}
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"spec": map[string]interface{}{},
			"status": map[string]interface{}{
				"conditions": conditions,
			},
		},
	}
}

// newMachineConfig creates a test machine config object
func newMachineConfig(name, namespace, kind string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "rke-machine-config.cattle.io/v1",
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{},
		},
	}
}

// newMachinePool creates a machine pool configuration for RKE clusters
func newMachinePool(name, nodeConfigName, nodeConfigKind string, quantity int) provisioningV1.RKEMachinePool {
	return provisioningV1.RKEMachinePool{
		Name:     name,
		Quantity: &[]int32{int32(quantity)}[0],
		NodeConfig: &corev1.ObjectReference{
			APIVersion: "rke-machine-config.cattle.io/v1",
			Kind:       nodeConfigKind,
			Name:       nodeConfigName,
		},
	}
}

// newK3kCluster creates a K3k virtual cluster
func newK3kCluster(name string, mode string, version string, servers int, agents int) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k3k.io/v1beta1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"mode":    mode,
				"servers": int64(servers),
				"version": version,
				"agents":  int64(agents),
			},
			"status": map[string]interface{}{
				"ready": true,
				"phase": "Running",
			},
		},
	}
}

func createDummyKDMData(versions ...string) string {
	m := make(map[string]interface{})
	m["resourceType"] = "releases"
	d := make([]interface{}, len(versions))
	for i, v := range versions {
		d[i] = map[string]interface{}{
			"id":      v,
			"type":    "release",
			"version": v,
		}
	}
	m["data"] = d
	result, _ := json.Marshal(m)
	return string(result)
}
