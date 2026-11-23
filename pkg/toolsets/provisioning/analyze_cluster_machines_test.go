package provisioning

import (
	"errors"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

var fakeCAPIMachine = &unstructured.Unstructured{
	Object: map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta1",
		"kind":       "Machine",
		"metadata": map[string]any{
			"name":      "test-cluster-machine-1",
			"namespace": "fleet-default",
			"labels": map[string]any{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
			"ownerReferences": []any{
				map[string]any{
					"apiVersion": "cluster.x-k8s.io/v1beta1",
					"kind":       "MachineSet",
					"name":       "test-cluster-machineset-1",
					"controller": true,
				},
			},
		},
		"spec": map[string]any{
			"clusterName": "test-cluster",
			"bootstrap": map[string]any{
				"configRef": map[string]any{
					"kind": "RKEBootstrap",
					"name": "test-cluster-machine-1",
				},
			},
		},
		"status": map[string]any{
			"phase": "Running",
		},
	},
}

var fakeCAPIMachine2 = &unstructured.Unstructured{
	Object: map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta1",
		"kind":       "Machine",
		"metadata": map[string]any{
			"name":      "test-cluster-machine-2",
			"namespace": "fleet-default",
			"labels": map[string]any{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
			"ownerReferences": []any{
				map[string]any{
					"apiVersion": "cluster.x-k8s.io/v1beta1",
					"kind":       "MachineSet",
					"name":       "test-cluster-machineset-1",
					"controller": true,
				},
			},
		},
		"spec": map[string]any{
			"clusterName": "test-cluster",
		},
		"status": map[string]any{
			"phase": "Provisioning",
		},
	},
}

var fakeCAPIMachineSet = &unstructured.Unstructured{
	Object: map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta1",
		"kind":       "MachineSet",
		"metadata": map[string]any{
			"name":      "test-cluster-machineset-1",
			"namespace": "fleet-default",
			"labels": map[string]any{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
			"ownerReferences": []any{
				map[string]any{
					"apiVersion": "cluster.x-k8s.io/v1beta1",
					"kind":       "MachineDeployment",
					"name":       "test-cluster-md-0",
					"controller": true,
				},
			},
		},
		"spec": map[string]any{
			"replicas": int64(2),
		},
		"status": map[string]any{
			"replicas":      int64(2),
			"readyReplicas": int64(1),
		},
	},
}

var fakeCAPIMachineDeployment = &unstructured.Unstructured{
	Object: map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta1",
		"kind":       "MachineDeployment",
		"metadata": map[string]any{
			"name":      "test-cluster-md-0",
			"namespace": "fleet-default",
			"labels": map[string]any{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
		"spec": map[string]any{
			"replicas": int64(2),
			"selector": map[string]any{
				"matchLabels": map[string]any{
					"cluster.x-k8s.io/cluster-name": "test-cluster",
				},
			},
		},
		"status": map[string]any{
			"replicas":      int64(2),
			"readyReplicas": int64(1),
		},
	},
}

func TestAnalyzeClusterMachines(t *testing.T) {

	tests := map[string]struct {
		params         inspectClusterMachinesParams
		objects        []runtime.Object
		expectedResult string
		expectedError  string
	}{
		"analyze cluster with machines, machine sets, and deployments": {
			params: inspectClusterMachinesParams{
				Cluster:                   "c-analyze-machines",
				Namespace:                 "fleet-default",
				IncludeMachineDeployments: true,
				IncludeMachineSets:        true,
				IncludeMachines:           true,
			},
			objects: []runtime.Object{fakeCAPIMachine, fakeCAPIMachine2, fakeCAPIMachineSet, fakeCAPIMachineDeployment},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machine-1",
							"namespace": "fleet-default"
						},
						"spec": {
							"bootstrap": {
								"configRef": {
									"kind": "RKEBootstrap",
									"name": "test-cluster-machine-1"
								}
							},
							"clusterName": "test-cluster"
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
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machine-2",
							"namespace": "fleet-default"
						},
						"spec": {
							"clusterName": "test-cluster"
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
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machineset-1",
							"namespace": "fleet-default"
						},
						"spec": {
							"replicas": 2
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 2
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
							"replicas": 2,
							"selector": {
								"matchLabels": {
									"cluster.x-k8s.io/cluster-name": "test-cluster"
								}
							}
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 2
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "test-cluster-machine-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					},
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "test-cluster-machine-2",
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
		"analyze cluster with no machines": {
			params: inspectClusterMachinesParams{
				Cluster:                   "empty-cluster",
				Namespace:                 "fleet-default",
				IncludeMachineDeployments: true,
				IncludeMachineSets:        true,
				IncludeMachines:           true,
			},
			objects:        []runtime.Object{newManagementCluster("empty-cluster", true)},
			expectedResult: `{"llm":"no resources found"}`,
		},
		"analyze cluster with default namespace": {
			params: inspectClusterMachinesParams{
				Cluster:                   "Production Cluster",
				Namespace:                 "",
				IncludeMachineDeployments: true,
				IncludeMachineSets:        true,
				IncludeMachines:           true,
			},
			objects: []runtime.Object{fakeCAPIMachine, fakeCAPIMachineSet, fakeCAPIMachineDeployment},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machine-1",
							"namespace": "fleet-default"
						},
						"spec": {
							"bootstrap": {
								"configRef": {
									"kind": "RKEBootstrap",
									"name": "test-cluster-machine-1"
								}
							},
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
							"namespace": "fleet-default"
						},
						"spec": {
							"replicas": 2
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 2
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
							"replicas": 2,
							"selector": {
								"matchLabels": {
									"cluster.x-k8s.io/cluster-name": "test-cluster"
								}
							}
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 2
						}
					}
				],
				"uiContext": [
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
		"analyze cluster with only machines (no sets or deployments, custom cluster)": {
			params: inspectClusterMachinesParams{
				Cluster:                   "test-cluster",
				Namespace:                 "fleet-default",
				IncludeMachineDeployments: true,
				IncludeMachineSets:        true,
				IncludeMachines:           true,
			},
			objects: []runtime.Object{fakeCAPIMachine},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machine-1",
							"namespace": "fleet-default"
						},
						"spec": {
							"bootstrap": {
								"configRef": {
									"kind": "RKEBootstrap",
									"name": "test-cluster-machine-1"
								}
							},
							"clusterName": "test-cluster"
						},
						"status": {
							"phase": "Running"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "test-cluster-machine-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					}
				]
			}`,
		},
		"analyze cluster machines when the tool is configured with a rancher URL": {
			params: inspectClusterMachinesParams{
				Cluster:                   "empty-cluster",
				Namespace:                 "fleet-default",
				IncludeMachineDeployments: true,
				IncludeMachineSets:        true,
				IncludeMachines:           true,
			},
			objects:        []runtime.Object{newManagementCluster("empty-cluster", true)},
			expectedResult: `{"llm":"no resources found"}`,
		},
		"analyze cluster with only machine deployments selected": {
			params: inspectClusterMachinesParams{
				Cluster:                   "test-cluster",
				Namespace:                 "fleet-default",
				IncludeMachineDeployments: true,
			},
			objects: []runtime.Object{fakeCAPIMachine, fakeCAPIMachineSet, fakeCAPIMachineDeployment},
			expectedResult: `{
				"llm": [
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
							"replicas": 2,
							"selector": {
								"matchLabels": {
									"cluster.x-k8s.io/cluster-name": "test-cluster"
								}
							}
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 2
						}
					}
				],
				"uiContext": [
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
		"analyze cluster with only machine sets selected": {
			params: inspectClusterMachinesParams{
				Cluster:            "test-cluster",
				Namespace:          "fleet-default",
				IncludeMachineSets: true,
			},
			objects: []runtime.Object{fakeCAPIMachine, fakeCAPIMachineSet, fakeCAPIMachineDeployment},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "MachineSet",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machineset-1",
							"namespace": "fleet-default"
						},
						"spec": {
							"replicas": 2
						},
						"status": {
							"readyReplicas": 1,
							"replicas": 2
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "MachineSet",
						"name": "test-cluster-machineset-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machineset"
					}
				]
			}`,
		},
		"analyze cluster with only machines selected": {
			params: inspectClusterMachinesParams{
				Cluster:         "test-cluster",
				Namespace:       "fleet-default",
				IncludeMachines: true,
			},
			objects: []runtime.Object{fakeCAPIMachine, fakeCAPIMachineSet, fakeCAPIMachineDeployment},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {
								"cluster.x-k8s.io/cluster-name": "test-cluster"
							},
							"name": "test-cluster-machine-1",
							"namespace": "fleet-default"
						},
						"spec": {
							"bootstrap": {
								"configRef": {
									"kind": "RKEBootstrap",
									"name": "test-cluster-machine-1"
								}
							},
							"clusterName": "test-cluster"
						},
						"status": {
							"phase": "Running"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "test-cluster-machine-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					}
				]
			}`,
		},
		"analyze cluster with no resource types selected": {
			params: inspectClusterMachinesParams{
				Cluster:   "test-cluster",
				Namespace: "fleet-default",
			},
			objects:       []runtime.Object{fakeCAPIMachine, fakeCAPIMachineSet, fakeCAPIMachineDeployment},
			expectedError: "at least one of includeMachineDeployments, includeMachineSets, or includeMachines must be true",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			managementCluster := newManagementCluster("c-analyze-machines", true)
			require.NoError(t, unstructured.SetNestedField(managementCluster.Object, "Production Cluster", "spec", "displayName"))
			objects := append([]runtime.Object{
				managementCluster,
				newProvisioningCluster("test-cluster", "fleet-default", managementCluster.GetName()),
			}, tt.objects...)
			dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), provisioningCustomListKinds(), objects...)
			tools := newTestTools(dynClient)
			result, _, err := tools.analyzeClusterMachines(middleware.WithToken(t.Context(), testToken), newToolRequest("analyze-cluster-machines"), tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				text, ok := result.Content[0].(*mcp.TextContent)
				assert.Truef(t, ok, "expected type *mcp.TextContent")
				assert.Truef(t, ok, "expected expectedResult to be a JSON string")
				assert.JSONEq(t, tt.expectedResult, text.Text)
			}
		})
	}
}

// withNodeRole sets spec.worker on a management node, which is dropped when the node is trimmed.
func withNodeRole(node *unstructured.Unstructured, worker bool) *unstructured.Unstructured {
	_ = unstructured.SetNestedField(node.Object, worker, "spec", "worker")
	return node
}

func TestAnalyzeClusterMachinesManagementNodes(t *testing.T) {
	const (
		fullNodeStatus = `"status": {
			"nodeName": %q,
			"conditions": [{"type": "Ready", "status": "True"}],
			"internalNodeStatus": {"capacity": {"cpu": "4"}}
		}`
		trimmedNodeStatus = `"status": {
			"nodeName": %q,
			"conditions": [{"type": "Ready", "status": "True"}]
		}`
	)

	nodeJSON := func(name, clusterID, nodeName string, full bool) string {
		status := trimmedNodeStatus
		extra := ""
		if full {
			status = fullNodeStatus
			extra = `"labels": {"example": "label"},`
		}
		spec := ""
		if full {
			spec = `"spec": {"worker": true},`
		}

		return fmt.Sprintf(`{
			"apiVersion": "management.cattle.io/v3",
			"kind": "Node",
			"metadata": {%s "name": %q, "namespace": %q},
			%s
			`+status+`
		}`, extra, name, clusterID, spec, nodeName)
	}
	uiContextJSON := func(name, clusterID string) string {
		return fmt.Sprintf(`{"cluster": "local", "kind": "Node", "name": %q, "namespace": %q, "type": "management.cattle.io.node"}`, name, clusterID)
	}

	trimmedNote := "Cluster %s has no CAPI machines, so its Rancher management node objects are returned instead. status.nodeName is the name of the Kubernetes node. Only the names and conditions of the nodes are included, set fullNodes to true for the complete objects."
	fullNote := "Cluster %s has no CAPI machines, so its Rancher management node objects are returned instead. status.nodeName is the name of the Kubernetes node."

	tests := map[string]struct {
		params         inspectClusterMachinesParams
		objects        []runtime.Object
		expectedResult string
		expectedError  string
	}{
		"nodes are trimmed by default": {
			params: inspectClusterMachinesParams{Cluster: "nodes-trimmed", IncludeMachines: true},
			objects: []runtime.Object{
				newManagementCluster("nodes-trimmed", true),
				newManagementNode("m-1", "nodes-trimmed", "worker-1"),
				newManagementNode("m-2", "nodes-trimmed", "worker-2"),
			},
			expectedResult: fmt.Sprintf(`{
				"llm": {
					"note": %q,
					"resources": [%s, %s]
				},
				"uiContext": [%s, %s]
			}`, fmt.Sprintf(trimmedNote, "nodes-trimmed"),
				nodeJSON("m-1", "nodes-trimmed", "worker-1", false), nodeJSON("m-2", "nodes-trimmed", "worker-2", false),
				uiContextJSON("m-1", "nodes-trimmed"), uiContextJSON("m-2", "nodes-trimmed")),
		},
		"full nodes are returned when requested": {
			params: inspectClusterMachinesParams{Cluster: "nodes-full", IncludeMachines: true, FullNodes: true},
			objects: []runtime.Object{
				newManagementCluster("nodes-full", true),
				newManagementNode("m-1", "nodes-full", "worker-1"),
			},
			expectedResult: fmt.Sprintf(`{
				"llm": {
					"note": %q,
					"resources": [%s]
				},
				"uiContext": [%s]
			}`, fmt.Sprintf(fullNote, "nodes-full"),
				nodeJSON("m-1", "nodes-full", "worker-1", true), uiContextJSON("m-1", "nodes-full")),
		},
		"only the nodes of the requested cluster are returned": {
			params: inspectClusterMachinesParams{Cluster: "nodes-scoped", IncludeMachines: true},
			objects: []runtime.Object{
				newManagementCluster("nodes-scoped", true),
				newManagementCluster("nodes-other", true),
				newManagementNode("m-1", "nodes-scoped", "worker-1"),
				newManagementNode("m-other", "nodes-other", "worker-other"),
			},
			expectedResult: fmt.Sprintf(`{
				"llm": {
					"note": %q,
					"resources": [%s]
				},
				"uiContext": [%s]
			}`, fmt.Sprintf(trimmedNote, "nodes-scoped"),
				nodeJSON("m-1", "nodes-scoped", "worker-1", false), uiContextJSON("m-1", "nodes-scoped")),
		},
		"the jsonPath filter is evaluated on the complete nodes before trimming": {
			// spec.worker is removed by trimming, so the filter only matches if it ran first
			params: inspectClusterMachinesParams{Cluster: "nodes-filter", IncludeMachines: true, JSONPath: `@.spec.worker==false`},
			objects: []runtime.Object{
				newManagementCluster("nodes-filter", true),
				newManagementNode("m-1", "nodes-filter", "worker-1"),
				withNodeRole(newManagementNode("m-2", "nodes-filter", "worker-2"), false),
			},
			expectedResult: fmt.Sprintf(`{
				"llm": {
					"note": %q,
					"resources": [%s]
				},
				"uiContext": [%s]
			}`, fmt.Sprintf(trimmedNote, "nodes-filter"),
				nodeJSON("m-2", "nodes-filter", "worker-2", false), uiContextJSON("m-2", "nodes-filter")),
		},
		"the jsonPath filter can exclude every node": {
			params: inspectClusterMachinesParams{Cluster: "nodes-filter-none", IncludeMachines: true, JSONPath: `@.status.nodeName=="missing"`},
			objects: []runtime.Object{
				newManagementCluster("nodes-filter-none", true),
				newManagementNode("m-1", "nodes-filter-none", "worker-1"),
			},
			expectedResult: fmt.Sprintf(`{
				"llm": {
					"note": %q,
					"resources": "no resources found"
				}
			}`, fmt.Sprintf(trimmedNote, "nodes-filter-none")),
		},
		"an invalid jsonPath filter is an error": {
			params: inspectClusterMachinesParams{Cluster: "nodes-filter-invalid", IncludeMachines: true, JSONPath: `@.status.nodeName==`},
			objects: []runtime.Object{
				newManagementCluster("nodes-filter-invalid", true),
				newManagementNode("m-1", "nodes-filter-invalid", "worker-1"),
			},
			expectedError: "invalid jsonPath filter",
		},
		"a cluster without machines or nodes has no note": {
			params:         inspectClusterMachinesParams{Cluster: "nodes-none", IncludeMachines: true},
			objects:        []runtime.Object{newManagementCluster("nodes-none", true)},
			expectedResult: `{"llm": "no resources found"}`,
		},
		"nodes are not looked up when machines are not requested": {
			params: inspectClusterMachinesParams{Cluster: "nodes-not-requested", IncludeMachineSets: true},
			objects: []runtime.Object{
				newManagementCluster("nodes-not-requested", true),
				newManagementNode("m-1", "nodes-not-requested", "worker-1"),
			},
			expectedResult: `{"llm": "no resources found"}`,
		},
		"nodes are not returned when the cluster has machines": {
			params: inspectClusterMachinesParams{Cluster: "test-cluster", IncludeMachines: true},
			objects: []runtime.Object{
				fakeCAPIMachine,
				newManagementCluster("test-cluster", true),
				newManagementNode("m-1", "test-cluster", "worker-1"),
			},
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "cluster.x-k8s.io/v1beta1",
						"kind": "Machine",
						"metadata": {
							"labels": {"cluster.x-k8s.io/cluster-name": "test-cluster"},
							"name": "test-cluster-machine-1",
							"namespace": "fleet-default"
						},
						"spec": {
							"bootstrap": {"configRef": {"kind": "RKEBootstrap", "name": "test-cluster-machine-1"}},
							"clusterName": "test-cluster"
						},
						"status": {"phase": "Running"}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Machine",
						"name": "test-cluster-machine-1",
						"namespace": "fleet-default",
						"type": "cluster.x-k8s.io.machine"
					}
				]
			}`,
		},
		"a cluster unknown to rancher is an error": {
			params:        inspectClusterMachinesParams{Cluster: "nodes-unknown-cluster", IncludeMachines: true},
			expectedError: "failed to resolve cluster nodes-unknown-cluster",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := newTestProvisioningTools(tt.objects...)
			result, _, err := tools.analyzeClusterMachines(middleware.WithToken(t.Context(), testToken), newToolRequest("analyze-cluster-machines"), tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "expected type *mcp.TextContent")
			assert.JSONEq(t, tt.expectedResult, text.Text)
		})
	}
}

func TestAnalyzeClusterMachinesManagementNodesListError(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), provisioningCustomListKinds(),
		newManagementCluster("nodes-list-error", true))
	dynClient.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("nodes are unavailable")
	})

	tools := newTestTools(dynClient)
	_, _, err := tools.analyzeClusterMachines(middleware.WithToken(t.Context(), testToken), newToolRequest("analyze-cluster-machines"),
		inspectClusterMachinesParams{Cluster: "nodes-list-error", IncludeMachines: true})

	assert.ErrorContains(t, err, "failed to list nodes of cluster nodes-list-error")
	assert.ErrorContains(t, err, "nodes are unavailable")
}
