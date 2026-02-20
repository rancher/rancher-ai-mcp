package provisioning

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestAnalyzeCluster(t *testing.T) {
	tests := map[string]struct {
		params        InspectClusterParams
		fakeClientset kubernetes.Interface
		fakeDynClient *dynamicfake.FakeDynamicClient
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"analyze cluster with all resources": {
			params: InspectClusterParams{
				Cluster:   "test-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("test-cluster", "fleet-default", "c-m-abc123"),
				newCAPICluster("test-cluster", "fleet-default"),
				newCAPIMachine("test-cluster-machine-1", "fleet-default", "test-cluster", "Running", "test-cluster-machineset-1"),
				newCAPIMachineSet("test-cluster-machineset-1", "fleet-default", "test-cluster", 1, 1, "test-cluster-md-0"),
				newCAPIMachineDeployment("test-cluster-md-0", "fleet-default", "test-cluster", 1, 1),
				newManagementCluster("c-m-abc123", true),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "test-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "c-m-abc123",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-abc123"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Cluster",
						"metadata": {
							"name": "test-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"controlPlaneEndpoint": {
								"host": "localhost",
								"port": 6443
							},
							"controlPlaneRef": {
								"apiVersion": "rke.cattle.io/v1",
								"kind": "RKEControlPlane",
								"name": "test-cluster",
								"namespace": "fleet-default"
							}
						},
						"status": {
							"phase": "Provisioned"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machine-1",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineSet",
									"name": "test-cluster-machineset-1"
								}
							]
						},
						"spec": {
							"clusterName": "test-cluster"
						},
						"status": {
							"phase": "Running"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineSet",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machineset-1",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineDeployment",
									"name": "test-cluster-md-0"
								}
							]
						},
						"spec": {
							"replicas": 1
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 1
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineDeployment",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-md-0",
							"namespace": "fleet-default"
						},
						"spec": {
							"replicas": 1,
							"selector": {
								"matchLabels": {
									"cluster.x-k8s.io/cluster-name": "test-cluster"
								}
							}
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 1
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "test-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-abc123",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "test-cluster",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "test-cluster-machine-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					},
					{
						"cluster": "local",
						"kind": "MachineSet",
						"name": "test-cluster-machineset-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machineset"
					},
					{
						"cluster": "local",
						"kind": "MachineDeployment",
						"name": "test-cluster-md-0",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machinedeployment"
					}
				]
			}`,
		},
		"analyze cluster without CAPI cluster": {
			params: InspectClusterParams{
				Cluster:   "test-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("test-cluster", "fleet-default", "c-m-abc123"),
				newManagementCluster("c-m-abc123", true),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "test-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "c-m-abc123",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-abc123"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "test-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-abc123",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					}
				]
			}`,
		},
		"analyze cluster without machines": {
			params: InspectClusterParams{
				Cluster:   "test-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("test-cluster", "fleet-default", "c-m-abc123"),
				newCAPICluster("test-cluster", "fleet-default"),
				newManagementCluster("c-m-abc123", true),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "test-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "c-m-abc123",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-abc123"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Cluster",
						"metadata": {
							"name": "test-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"controlPlaneEndpoint": {
								"host": "localhost",
								"port": 6443
							},
							"controlPlaneRef": {
								"apiVersion": "rke.cattle.io/v1",
								"kind": "RKEControlPlane",
								"name": "test-cluster",
								"namespace": "fleet-default"
							}
						},
						"status": {
							"phase": "Provisioned"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "test-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-abc123",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "test-cluster",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.cluster"
					}
				]
			}`,
		},
		"analyze local cluster with default namespace": {
			params: InspectClusterParams{
				Cluster:   "local",
				Namespace: "",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("local", "fleet-local", "local"),
				newManagementCluster("local", true),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "local",
							"namespace": "fleet-local"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "local",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "local"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "local",
						"namespace": "fleet-local",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "local",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					}
				]
			}`,
		},
		"analyze cluster not found": {
			params: InspectClusterParams{
				Cluster:   "nonexistent-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds()),
			expectedError: "provisioning cluster nonexistent-cluster not found in namespace fleet-default",
		},
		"analyze cluster with multiple machines and sets": {
			params: InspectClusterParams{
				Cluster:   "multi-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("multi-cluster", "fleet-default", "c-m-multi123"),
				newManagementCluster("c-m-multi123", true),
				newCAPICluster("multi-cluster", "fleet-default"),
				newCAPIMachine("multi-cluster-machine-1", "fleet-default", "multi-cluster", "Running", "multi-cluster-machineset-1"),
				newCAPIMachine("multi-cluster-machine-2", "fleet-default", "multi-cluster", "Running", "multi-cluster-machineset-1"),
				newCAPIMachine("multi-cluster-machine-3", "fleet-default", "multi-cluster", "Provisioning", "multi-cluster-machineset-2"),
				newCAPIMachineSet("multi-cluster-machineset-1", "fleet-default", "multi-cluster", 2, 2, "multi-cluster-md-0"),
				newCAPIMachineSet("multi-cluster-machineset-2", "fleet-default", "multi-cluster", 1, 0, "multi-cluster-md-1"),
				newCAPIMachineDeployment("multi-cluster-md-0", "fleet-default", "multi-cluster", 2, 2),
				newCAPIMachineDeployment("multi-cluster-md-1", "fleet-default", "multi-cluster", 1, 0),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "multi-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "c-m-multi123",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-multi123"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Cluster",
						"metadata": {
							"name": "multi-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"controlPlaneEndpoint": {
								"host": "localhost",
								"port": 6443
							},
							"controlPlaneRef": {
								"apiVersion": "rke.cattle.io/v1",
								"kind": "RKEControlPlane",
								"name": "multi-cluster",
								"namespace": "fleet-default"
							}
						},
						"status": {
							"phase": "Provisioned"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "multi-cluster"
							},
							"name": "multi-cluster-machine-1",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineSet",
									"name": "multi-cluster-machineset-1"
								}
							]
						},
						"spec": {
							"clusterName": "multi-cluster"
						},
						"status": {
							"phase": "Running"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "multi-cluster"
							},
							"name": "multi-cluster-machine-2",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineSet",
									"name": "multi-cluster-machineset-1"
								}
							]
						},
						"spec": {
							"clusterName": "multi-cluster"
						},
						"status": {
							"phase": "Running"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "multi-cluster"
							},
							"name": "multi-cluster-machine-3",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineSet",
									"name": "multi-cluster-machineset-2"
								}
							]
						},
						"spec": {
							"clusterName": "multi-cluster"
						},
						"status": {
							"phase": "Provisioning"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineSet",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "multi-cluster"
							},
							"name": "multi-cluster-machineset-1",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineDeployment",
									"name": "multi-cluster-md-0"
								}
							]
						},
						"spec": {
							"replicas": 2
						},
						"status": {
							"readyReplicas": 2,
							"replicas": 2
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineSet",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "multi-cluster"
							},
							"name": "multi-cluster-machineset-2",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineDeployment",
									"name": "multi-cluster-md-1"
								}
							]
						},
						"spec": {
							"replicas": 1
						},
						"status": {
							"readyReplicas": 0,
							"replicas": 1
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineDeployment",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "multi-cluster"
							},
							"name": "multi-cluster-md-0",
							"namespace": "fleet-default"
						},
						"spec": {
							"replicas": 2,
							"selector": {
								"matchLabels": {
									"cluster.x-k8s.io/cluster-name": "multi-cluster"
								}
							}
						},
						"status": {
							"readyReplicas": 2,
							"replicas": 2
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineDeployment",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "multi-cluster"
							},
							"name": "multi-cluster-md-1",
							"namespace": "fleet-default"
						},
						"spec": {
							"replicas": 1,
							"selector": {
								"matchLabels": {
									"cluster.x-k8s.io/cluster-name": "multi-cluster"
								}
							}
						},
						"status": {
							"readyReplicas": 0,
							"replicas": 1
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "multi-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-multi123",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "multi-cluster",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "multi-cluster-machine-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					},
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "multi-cluster-machine-2",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					},
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "multi-cluster-machine-3",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					},
					{
						"cluster": "local",
						"kind": "MachineSet",
						"name": "multi-cluster-machineset-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machineset"
					},
					{
						"cluster": "local",
						"kind": "MachineSet",
						"name": "multi-cluster-machineset-2",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machineset"
					},
					{
						"cluster": "local",
						"kind": "MachineDeployment",
						"name": "multi-cluster-md-0",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machinedeployment"
					},
					{
						"cluster": "local",
						"kind": "MachineDeployment",
						"name": "multi-cluster-md-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machinedeployment"
					}
				]
			}`,
		},
		"analyze cluster with not-ready management cluster": {
			params: InspectClusterParams{
				Cluster:   "unhealthy-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("unhealthy-cluster", "fleet-default", "c-m-unhealthy"),
				newManagementCluster("c-m-unhealthy", false),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "unhealthy-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "c-m-unhealthy",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-unhealthy"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "False",
									"type": "Ready"
								}
							]
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "unhealthy-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-unhealthy",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					}
				]
			}`,
		},
		"analyze cluster with RKE machine pools and machine configs": {
			params: InspectClusterParams{
				Cluster:   "rke-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningClusterWithRKEConfig("rke-cluster", "fleet-default", "c-m-rke123", []provisioningV1.RKEMachinePool{
					newMachinePool("pool1", "rke-cluster-pool1-config", "Amazonec2Config", 3),
				}),
				newCAPICluster("rke-cluster", "fleet-default"),
				newCAPIMachine("rke-cluster-machine-1", "fleet-default", "rke-cluster", "Running", "rke-cluster-machineset-1"),
				newCAPIMachineSet("rke-cluster-machineset-1", "fleet-default", "rke-cluster", 1, 1, "rke-cluster-md-0"),
				newCAPIMachineDeployment("rke-cluster-md-0", "fleet-default", "rke-cluster", 1, 1),
				newMachineConfig("rke-cluster-pool1-config", "fleet-default", "Amazonec2Config"),
				newManagementCluster("c-m-rke123", true),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "rke-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {},
							"rkeConfig": {
								"chartValues": null,
								"dataDirectories": {},
								"machineGlobalConfig": null,
								"machinePoolDefaults": {},
								"machinePools": [
									{
										"machineConfigRef": {
											"apiVersion": "rke-machine-config.cattle.io/v1",
											"kind": "Amazonec2Config",
											"name": "rke-cluster-pool1-config"
										},
										"name": "pool1",
										"quantity": 3
									}
								],
								"upgradeStrategy": {
									"controlPlaneDrainOptions": {
										"deleteEmptyDirData": false,
										"disableEviction": false,
										"enabled": false,
										"force": false,
										"gracePeriod": 0,
										"ignoreDaemonSets": null,
										"ignoreErrors": false,
										"postDrainHooks": null,
										"preDrainHooks": null,
										"skipWaitForDeleteTimeoutSeconds": 0,
										"timeout": 0
									},
									"workerDrainOptions": {
										"deleteEmptyDirData": false,
										"disableEviction": false,
										"enabled": false,
										"force": false,
										"gracePeriod": 0,
										"ignoreDaemonSets": null,
										"ignoreErrors": false,
										"postDrainHooks": null,
										"preDrainHooks": null,
										"skipWaitForDeleteTimeoutSeconds": 0,
										"timeout": 0
									}
								}
							}
						},
						"status": {
							"clusterName": "c-m-rke123",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-rke123"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Cluster",
						"metadata": {
							"name": "rke-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"controlPlaneEndpoint": {
								"host": "localhost",
								"port": 6443
							},
							"controlPlaneRef": {
								"apiVersion": "rke.cattle.io/v1",
								"kind": "RKEControlPlane",
								"name": "rke-cluster",
								"namespace": "fleet-default"
							}
						},
						"status": {
							"phase": "Provisioned"
						}
					},
					{
						"apiVersion": "rke-machine-config.cattle.io/v1",
						"kind": "Amazonec2Config",
						"metadata": {
							"name": "rke-cluster-pool1-config",
							"namespace": "fleet-default"
						},
						"spec": {}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "rke-cluster"
							},
							"name": "rke-cluster-machine-1",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineSet",
									"name": "rke-cluster-machineset-1"
								}
							]
						},
						"spec": {
							"clusterName": "rke-cluster"
						},
						"status": {
							"phase": "Running"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineSet",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "rke-cluster"
							},
							"name": "rke-cluster-machineset-1",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineDeployment",
									"name": "rke-cluster-md-0"
								}
							]
						},
						"spec": {
							"replicas": 1
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 1
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineDeployment",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "rke-cluster"
							},
							"name": "rke-cluster-md-0",
							"namespace": "fleet-default"
						},
						"spec": {
							"replicas": 1,
							"selector": {
								"matchLabels": {
									"cluster.x-k8s.io/cluster-name": "rke-cluster"
								}
							}
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 1
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "rke-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-rke123",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "rke-cluster",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Amazonec2Config",
						"name": "rke-cluster-pool1-config",
						"namespace": "fleet-default",
						"type": "rke-machine-config.cattle.io.amazonec2config"
					},
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "rke-cluster-machine-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					},
					{
						"cluster": "local",
						"kind": "MachineSet",
						"name": "rke-cluster-machineset-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machineset"
					},
					{
						"cluster": "local",
						"kind": "MachineDeployment",
						"name": "rke-cluster-md-0",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machinedeployment"
					}
				]
			}`,
		},
		"analyze cluster with namespace not set (defaults to fleet-default)": {
			params: InspectClusterParams{
				Cluster:   "test-cluster",
				Namespace: "",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("test-cluster", "fleet-default", "c-m-abc123"),
				newManagementCluster("c-m-abc123", true),
				newCAPICluster("test-cluster", "fleet-default"),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "test-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "c-m-abc123",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-abc123"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Cluster",
						"metadata": {
							"name": "test-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"controlPlaneEndpoint": {
								"host": "localhost",
								"port": 6443
							},
							"controlPlaneRef": {
								"apiVersion": "rke.cattle.io/v1",
								"kind": "RKEControlPlane",
								"name": "test-cluster",
								"namespace": "fleet-default"
							}
						},
						"status": {
							"phase": "Provisioned"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "test-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-abc123",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "test-cluster",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.cluster"
					}
				]
			}`,
		},
		"analyze cluster with only provisioning and management cluster": {
			params: InspectClusterParams{
				Cluster:   "minimal-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("minimal-cluster", "fleet-default", "c-m-minimal"),
				newManagementCluster("c-m-minimal", true),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "minimal-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "c-m-minimal",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-minimal"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "minimal-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-minimal",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					}
				]
			}`,
		},
		"analyze cluster with single machine and full hierarchy": {
			params: InspectClusterParams{
				Cluster:   "single-machine-cluster",
				Namespace: "fleet-default",
			},
			requestURL:    testURL,
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("single-machine-cluster", "fleet-default", "c-m-single"),
				newManagementCluster("c-m-single", true),
				newCAPICluster("single-machine-cluster", "fleet-default"),
				newCAPIMachine("single-machine-cluster-machine-1", "fleet-default", "single-machine-cluster", "Running", "single-machine-cluster-machineset-1"),
				newCAPIMachineSet("single-machine-cluster-machineset-1", "fleet-default", "single-machine-cluster", 1, 1, "single-machine-cluster-md-0"),
				newCAPIMachineDeployment("single-machine-cluster-md-0", "fleet-default", "single-machine-cluster", 1, 1),
			),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "provisioning.cattle.io/v1",
						"kind": "Cluster",
						"metadata": {
							"name": "single-machine-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"localClusterAuthEndpoint": {}
						},
						"status": {
							"clusterName": "c-m-single",
							"observedGeneration": 0,
							"ready": true
						}
					},
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Cluster",
						"metadata": {
							"name": "c-m-single"
						},
						"spec": {},
						"status": {
							"conditions": [
								{
									"status": "True",
									"type": "Ready"
								}
							]
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Cluster",
						"metadata": {
							"name": "single-machine-cluster",
							"namespace": "fleet-default"
						},
						"spec": {
							"controlPlaneEndpoint": {
								"host": "localhost",
								"port": 6443
							},
							"controlPlaneRef": {
								"apiVersion": "rke.cattle.io/v1",
								"kind": "RKEControlPlane",
								"name": "single-machine-cluster",
								"namespace": "fleet-default"
							}
						},
						"status": {
							"phase": "Provisioned"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "single-machine-cluster"
							},
							"name": "single-machine-cluster-machine-1",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineSet",
									"name": "single-machine-cluster-machineset-1"
								}
							]
						},
						"spec": {
							"clusterName": "single-machine-cluster"
						},
						"status": {
							"phase": "Running"
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineSet",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "single-machine-cluster"
							},
							"name": "single-machine-cluster-machineset-1",
							"namespace": "fleet-default",
							"ownerReferences": [
								{
									"apiVersion": "cluster.x-k8s.io/v1beta1",
									"controller": true,
									"kind": "MachineDeployment",
									"name": "single-machine-cluster-md-0"
								}
							]
						},
						"spec": {
							"replicas": 1
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 1
						}
					},
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineDeployment",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "single-machine-cluster"
							},
							"name": "single-machine-cluster-md-0",
							"namespace": "fleet-default"
						},
						"spec": {
							"replicas": 1,
							"selector": {
								"matchLabels": {
									"cluster.x-k8s.io/cluster-name": "single-machine-cluster"
								}
							}
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 1
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "single-machine-cluster",
						"namespace": "fleet-default",
						"type": "provisioning.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "c-m-single",
						"namespace": "",
						"type": "management.cattle.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Cluster",
						"name": "single-machine-cluster",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.cluster"
					},
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "single-machine-cluster-machine-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					},
					{
						"cluster": "local",
						"kind": "MachineSet",
						"name": "single-machine-cluster-machineset-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machineset"
					},
					{
						"cluster": "local",
						"kind": "MachineDeployment",
						"name": "single-machine-cluster-md-0",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machinedeployment"
					}
				]
			}`,
		},
		"analyze cluster - no rancherURL or request URL": {
			params: InspectClusterParams{
				Cluster:   "test-cluster",
				Namespace: "fleet-default",
			},
			fakeClientset: newFakeClientsetWithCAPIDiscovery(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(capiMachineScheme(), capiCustomListKinds(),
				newProvisioningCluster("test-cluster", "fleet-default", "c-m-abc123"),
				newManagementCluster("c-m-abc123", true),
			),
			expectedError: "no URL for rancher request",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{
				ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
					return tt.fakeClientset, nil
				},
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return tt.fakeDynClient, nil
				},
			}
			tools := NewTools(test.WrapClient(c, testToken, testURL), tt.rancherURL)
			req := test.NewCallToolRequest(tt.requestURL)
			req.Params = &mcp.CallToolParamsRaw{Name: "analyze-cluster"}

			result, _, err := tools.AnalyzeCluster(middleware.WithToken(t.Context(), testToken), req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NoError(t, err)

				text, ok := result.Content[0].(*mcp.TextContent)
				assert.Truef(t, ok, "expected type *mcp.TextContent")

				assert.Truef(t, ok, "expected expectedResult to be a JSON string")
				assert.JSONEq(t, tt.expectedResult, text.Text)
			}
		})
	}
}
