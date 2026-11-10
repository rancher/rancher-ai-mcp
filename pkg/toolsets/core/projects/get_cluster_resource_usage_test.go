package projects

import (
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// nodeMetricsGVR is the GVR used by the metrics server API for node metrics.
// The resource name is "nodes" under the metrics.k8s.io group, which differs
// from the type-based pluralization ("nodemetricses"), so metrics objects must
// be added to the tracker explicitly with this GVR.
var nodeMetricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}

// newClusterResourceUsageTools builds a Tools instance backed by a fake dynamic client
// pre-populated with the given objects and node metrics. If metricsErr is set, listing node metrics
// returns it, as it would when the metrics server is not installed or the user can't read metrics.
func newClusterResourceUsageTools(t *testing.T, fakeToken string, objects []runtime.Object, metricsObjects []runtime.Object, metricsErr error) *Tools {
	t.Helper()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = metav1.AddMetaToScheme(scheme)
	_ = metricsv1beta1.AddToScheme(scheme)

	customListKinds := map[schema.GroupVersionResource]string{
		{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}: "ClusterList",
		nodeMetricsGVR: "NodeMetricsList",
	}

	fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, customListKinds, objects...)
	for _, obj := range metricsObjects {
		err := fakeDynClient.Tracker().Create(nodeMetricsGVR, obj, "")
		require.NoError(t, err)
	}
	if metricsErr != nil {
		fakeDynClient.PrependReactor("list", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
			if action.GetResource() == nodeMetricsGVR {
				return true, nil, metricsErr
			}
			return false, nil, nil
		})
	}

	c := &client.Client{
		DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) {
			return fakeDynClient, nil
		},
	}
	return NewTools(test.WrapClient(c, fakeToken), false)
}

func TestGetClusterResourceUsage(t *testing.T) {
	fakeURL := "https://localhost:8080"
	fakeToken := "fakeToken"

	cluster := fakeMgmtCluster("test-cluster")

	objects := []runtime.Object{
		cluster,
		fakeNode("node-1", true, false, map[string]string{
			"node-role.kubernetes.io/control-plane": "true",
			"node-role.kubernetes.io/etcd":          "true",
		}, "4", "8Gi", "110"),
		fakeNode("node-2", false, true, map[string]string{
			"node-role.kubernetes.io/worker": "true",
		}, "2", "4Gi", "110"),
		fakeScheduledPod("running-pod", "ns-1", "node-1", corev1.PodRunning,
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("2Gi")},
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
		),
		fakeScheduledPod("pending-pod", "ns-2", "node-2", corev1.PodPending,
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			corev1.ResourceList{},
		),
		fakeScheduledPod("completed-pod", "ns-1", "node-1", corev1.PodSucceeded,
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("3"), corev1.ResourceMemory: resource.MustParse("3Gi")},
			corev1.ResourceList{},
		),
		fakeScheduledPod("unscheduled-pod", "ns-2", "", corev1.PodPending,
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("16Gi")},
			corev1.ResourceList{},
		),
		// Ignored, as failed pods no longer hold resources on their node.
		fakeScheduledPod("failed-pod", "ns-2", "node-2", corev1.PodFailed,
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			corev1.ResourceList{},
		),
		// Ignored, as the pod is bound to a node that no longer exists.
		fakeScheduledPod("orphaned-pod", "ns-1", "deleted-node", corev1.PodRunning,
			corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			corev1.ResourceList{},
		),
	}

	tests := map[string]struct {
		cluster        string
		objects        []runtime.Object
		metrics        []runtime.Object
		metricsErr     error
		expectedResult string
		expectedError  bool
	}{
		"nodes with metrics": {
			cluster: "test-cluster",
			objects: objects,
			metrics: []runtime.Object{
				fakeNodeMetrics("node-1", "2", "4Gi"),
				fakeNodeMetrics("node-2", "500000000n", "1Gi"),
			},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"metricsAvailable": true,
					"totals": {
						"nodeCount": 2,
						"readyNodeCount": 1,
						"unscheduledPodCount": 1,
						"cpu": {
							"capacity": "6", "allocatable": "6", "requests": "1500m", "limits": "2", "usage": "2500m",
							"requestsPercent": 25, "limitsPercent": 33.3, "usagePercent": 41.7
						},
						"memory": {
							"capacity": "12Gi", "allocatable": "12Gi", "requests": "3Gi", "limits": "4Gi", "usage": "5Gi",
							"requestsPercent": 25, "limitsPercent": 33.3, "usagePercent": 41.7
						},
						"pods": {"allocatable": 220, "count": 2, "percent": 0.9}
					},
					"nodes": [
						{
							"name": "node-1",
							"roles": ["control-plane", "etcd"],
							"ready": true,
							"unschedulable": false,
							"cpu": {
								"capacity": "4", "allocatable": "4", "requests": "1", "limits": "2", "usage": "2",
								"requestsPercent": 25, "limitsPercent": 50, "usagePercent": 50
							},
							"memory": {
								"capacity": "8Gi", "allocatable": "8Gi", "requests": "2Gi", "limits": "4Gi", "usage": "4Gi",
								"requestsPercent": 25, "limitsPercent": 50, "usagePercent": 50
							},
							"pods": {"allocatable": 110, "count": 1, "percent": 0.9}
						},
						{
							"name": "node-2",
							"roles": ["worker"],
							"ready": false,
							"unschedulable": true,
							"cpu": {
								"capacity": "2", "allocatable": "2", "requests": "500m", "limits": "0", "usage": "500m",
								"requestsPercent": 25, "limitsPercent": 0, "usagePercent": 25
							},
							"memory": {
								"capacity": "4Gi", "allocatable": "4Gi", "requests": "1Gi", "limits": "0", "usage": "1Gi",
								"requestsPercent": 25, "limitsPercent": 0, "usagePercent": 25
							},
							"pods": {"allocatable": 110, "count": 1, "percent": 0.9}
						}
					]
				}
		}`,
		},
		"not ready node without metrics": {
			cluster: "test-cluster",
			objects: objects,
			metrics: []runtime.Object{fakeNodeMetrics("node-1", "2", "4Gi")},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"metricsAvailable": true,
					"nodesWithoutMetrics": ["node-2"],
					"totals": {
						"nodeCount": 2,
						"readyNodeCount": 1,
						"unscheduledPodCount": 1,
						"cpu": {
							"capacity": "6", "allocatable": "6", "requests": "1500m", "limits": "2",
							"requestsPercent": 25, "limitsPercent": 33.3
						},
						"memory": {
							"capacity": "12Gi", "allocatable": "12Gi", "requests": "3Gi", "limits": "4Gi",
							"requestsPercent": 25, "limitsPercent": 33.3
						},
						"pods": {"allocatable": 220, "count": 2, "percent": 0.9}
					},
					"nodes": [
						{
							"name": "node-1",
							"roles": ["control-plane", "etcd"],
							"ready": true,
							"unschedulable": false,
							"cpu": {
								"capacity": "4", "allocatable": "4", "requests": "1", "limits": "2", "usage": "2",
								"requestsPercent": 25, "limitsPercent": 50, "usagePercent": 50
							},
							"memory": {
								"capacity": "8Gi", "allocatable": "8Gi", "requests": "2Gi", "limits": "4Gi", "usage": "4Gi",
								"requestsPercent": 25, "limitsPercent": 50, "usagePercent": 50
							},
							"pods": {"allocatable": 110, "count": 1, "percent": 0.9}
						},
						{
							"name": "node-2",
							"roles": ["worker"],
							"ready": false,
							"unschedulable": true,
							"cpu": {
								"capacity": "2", "allocatable": "2", "requests": "500m", "limits": "0",
								"requestsPercent": 25, "limitsPercent": 0
							},
							"memory": {
								"capacity": "4Gi", "allocatable": "4Gi", "requests": "1Gi", "limits": "0",
								"requestsPercent": 25, "limitsPercent": 0
							},
							"pods": {"allocatable": 110, "count": 1, "percent": 0.9}
						}
					]
				}
		}`,
		},
		"without metrics server": {
			cluster:    "test-cluster",
			objects:    objects,
			metricsErr: errors.New("the server could not find the requested resource"),
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"metricsAvailable": false,
					"metricsUnavailableReason": "node metrics could not be retrieved, the metrics server might not be installed or available: the server could not find the requested resource",
					"totals": {
						"nodeCount": 2,
						"readyNodeCount": 1,
						"unscheduledPodCount": 1,
						"cpu": {
							"capacity": "6", "allocatable": "6", "requests": "1500m", "limits": "2",
							"requestsPercent": 25, "limitsPercent": 33.3
						},
						"memory": {
							"capacity": "12Gi", "allocatable": "12Gi", "requests": "3Gi", "limits": "4Gi",
							"requestsPercent": 25, "limitsPercent": 33.3
						},
						"pods": {"allocatable": 220, "count": 2, "percent": 0.9}
					},
					"nodes": [
						{
							"name": "node-1",
							"roles": ["control-plane", "etcd"],
							"ready": true,
							"unschedulable": false,
							"cpu": {
								"capacity": "4", "allocatable": "4", "requests": "1", "limits": "2",
								"requestsPercent": 25, "limitsPercent": 50
							},
							"memory": {
								"capacity": "8Gi", "allocatable": "8Gi", "requests": "2Gi", "limits": "4Gi",
								"requestsPercent": 25, "limitsPercent": 50
							},
							"pods": {"allocatable": 110, "count": 1, "percent": 0.9}
						},
						{
							"name": "node-2",
							"roles": ["worker"],
							"ready": false,
							"unschedulable": true,
							"cpu": {
								"capacity": "2", "allocatable": "2", "requests": "500m", "limits": "0",
								"requestsPercent": 25, "limitsPercent": 0
							},
							"memory": {
								"capacity": "4Gi", "allocatable": "4Gi", "requests": "1Gi", "limits": "0",
								"requestsPercent": 25, "limitsPercent": 0
							},
							"pods": {"allocatable": 110, "count": 1, "percent": 0.9}
						}
					]
				}
		}`,
		},
		"cluster with no nodes": {
			cluster: "test-cluster",
			objects: []runtime.Object{cluster},
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"metricsAvailable": true,
					"totals": {
						"nodeCount": 0,
						"readyNodeCount": 0,
						"unscheduledPodCount": 0,
						"cpu": {
							"capacity": "0", "allocatable": "0", "requests": "0", "limits": "0", "usage": "0",
							"requestsPercent": 0, "limitsPercent": 0, "usagePercent": 0
						},
						"memory": {
							"capacity": "0", "allocatable": "0", "requests": "0", "limits": "0", "usage": "0",
							"requestsPercent": 0, "limitsPercent": 0, "usagePercent": 0
						},
						"pods": {"allocatable": 0, "count": 0, "percent": 0}
					},
					"nodes": []
				}
		}`,
		},
		"user not allowed to read metrics": {
			cluster:    "test-cluster",
			objects:    []runtime.Object{cluster},
			metricsErr: apierrors.NewForbidden(schema.GroupResource{Group: "metrics.k8s.io", Resource: "nodes"}, "", errors.New("access denied")),
			expectedResult: `{
				"llm": {
					"cluster": "test-cluster",
					"metricsAvailable": false,
					"metricsUnavailableReason": "the user is not allowed to read node metrics from the metrics.k8s.io API",
					"totals": {
						"nodeCount": 0,
						"readyNodeCount": 0,
						"unscheduledPodCount": 0,
						"cpu": {
							"capacity": "0", "allocatable": "0", "requests": "0", "limits": "0",
							"requestsPercent": 0, "limitsPercent": 0
						},
						"memory": {
							"capacity": "0", "allocatable": "0", "requests": "0", "limits": "0",
							"requestsPercent": 0, "limitsPercent": 0
						},
						"pods": {"allocatable": 0, "count": 0, "percent": 0}
					},
					"nodes": []
				}
			}`,
		},
		"cluster not found": {
			cluster:       "missing-cluster",
			expectedError: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := newClusterResourceUsageTools(t, fakeToken, tt.objects, tt.metrics, tt.metricsErr)

			result, _, err := tools.getClusterResourceUsage(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				getClusterResourceUsageParams{Cluster: tt.cluster},
			)

			if tt.expectedError {
				require.Error(t, err)
				assert.Nil(t, result)
				return
			}
			require.NoError(t, err)
			require.Len(t, result.Content, 1)
			require.IsType(t, &mcp.TextContent{}, result.Content[0])
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

// fakeNode creates a Node with the given ready state, schedulability, labels, and capacity.
// Allocatable resources are set to the same values as the capacity.
func fakeNode(name string, ready, unschedulable bool, labels map[string]string, cpu, memory, pods string) *corev1.Node {
	readyStatus := corev1.ConditionFalse
	if ready {
		readyStatus = corev1.ConditionTrue
	}
	resources := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
		corev1.ResourcePods:   resource.MustParse(pods),
	}

	return &corev1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			Unschedulable: unschedulable,
		},
		Status: corev1.NodeStatus{
			Capacity:    resources,
			Allocatable: resources,
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: readyStatus},
			},
		},
	}
}

// fakeScheduledPod creates a Pod in the given phase, bound to the given node.
func fakeScheduledPod(name, namespace, nodeName string, phase corev1.PodPhase, requests, limits corev1.ResourceList) *corev1.Pod {
	pod := fakePodWithPhase(name, namespace, phase, requests, limits)
	pod.Spec.NodeName = nodeName
	return pod
}

// fakeNodeMetrics creates a NodeMetrics object with the given usage.
func fakeNodeMetrics(nodeName, cpu, memory string) *metricsv1beta1.NodeMetrics {
	return &metricsv1beta1.NodeMetrics{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "metrics.k8s.io/v1beta1",
			Kind:       "NodeMetrics",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
		Usage: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(memory),
		},
	}
}

func TestFormatCPU(t *testing.T) {
	tests := map[string]string{
		"0":           "0",
		"200m":        "200m",
		"2":           "2",
		"1500m":       "1500m",
		"1097324069n": "1098m",
		"250000u":     "250m",
		"1n":          "1m",
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			q := resource.MustParse(in)
			assert.Equal(t, want, formatCPU(&q))
		})
	}
}

func TestFormatMemory(t *testing.T) {
	tests := map[string]string{
		"0":          "0",
		"512":        "512",
		"1024":       "1Ki",
		"198Mi":      "198Mi",
		"1536Mi":     "1.5Gi",
		"61392032Ki": "58.5Gi",
		"1048575Ki":  "1Gi",
		"4Ti":        "4Ti",
		"2G":         "1.9Gi",
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			q := resource.MustParse(in)
			assert.Equal(t, want, formatMemory(&q))
		})
	}
}
