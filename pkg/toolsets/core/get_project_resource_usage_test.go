package core

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// newProjectResourceUsageTools builds a Tools instance backed by a fake dynamic client
// pre-populated with the given objects. Pod metrics objects must be passed separately
// via metricsObjects because the metrics API uses resource name "pods" under
// metrics.k8s.io, which differs from the type-based pluralization used by the tracker.
func newProjectResourceUsageTools(t *testing.T, fakeToken, fakeURL, rancherURL string, objects []runtime.Object, metricsObjects []runtime.Object) *Tools {
	t.Helper()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = metav1.AddMetaToScheme(scheme)
	_ = metricsv1beta1.AddToScheme(scheme)

	customListKinds := map[schema.GroupVersionResource]string{
		{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}: "ClusterList",
		{Group: "management.cattle.io", Version: "v3", Resource: "projects"}: "ProjectList",
		{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}:      "PodMetricsList",
	}
	// podMetricsGVR is the GVR used by the metrics server API for pod metrics.
	// The resource name is "pods" under the metrics.k8s.io group, which differs
	// from the type-based pluralization ("podmetrics"), so metrics objects must
	// be added to the tracker explicitly with this GVR.
	podMetricsGVR := schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}

	fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, customListKinds, objects...)
	for _, obj := range metricsObjects {
		err := fakeDynClient.Tracker().Create(podMetricsGVR, obj, obj.(metav1.Object).GetNamespace())
		require.NoError(t, err)
	}
	c := &client.Client{
		DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) {
			return fakeDynClient, nil
		},
	}
	return NewTools(test.WrapClient(c, fakeToken, fakeURL), rancherURL)
}

func TestGetProjectResourceUsage(t *testing.T) {
	fakeURL := "https://localhost:8080"
	fakeToken := "fakeToken"

	cluster := fakeMgmtCluster("test-cluster")
	project := fakeMgmtProject("test-cluster", "my-project", "My Project")
	ns1 := fakeProjectNamespace("ns-1", "my-project")

	t.Run("running pod with metrics", func(t *testing.T) {
		tools := newProjectResourceUsageTools(t, fakeToken, fakeURL, "",
			[]runtime.Object{
				cluster,
				project,
				ns1,
				fakeRunningPod("pod-1", "ns-1",
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				),
			},
			[]runtime.Object{
				fakePodMetrics("pod-1", "ns-1", corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				}),
			},
		)

		result, _, err := tools.getProjectResourceUsage(
			middleware.WithToken(t.Context(), fakeToken),
			test.NewCallToolRequest(fakeURL),
			getProjectResourceUsageParams{Name: "my-project", Cluster: "test-cluster"},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		text := result.Content[0].(*mcp.TextContent).Text
		t.Logf("got result: %s", text)
		assert.JSONEq(t, `{
			"llm": {
				"projectResourceUsageSummary": {
					"project": {
						"name": "my-project",
						"displayName": "My Project",
						"cluster": "test-cluster",
						"totals": {
							"podCount": 1,
							"cpu": {"requests": "100m", "limits": "200m", "usage": "50m"},
							"memory": {"requests": "128Mi", "limits": "256Mi", "usage": "64Mi"}
						}
					},
					"namespaces": {
						"ns-1": {
							"namespace": "ns-1",
							"cluster": "test-cluster",
							"totals": {
								"podCount": 1,
								"cpu": {"requests": "100m", "limits": "200m", "usage": "50m"},
								"memory": {"requests": "128Mi", "limits": "256Mi", "usage": "64Mi"}
							}
						}
					}
				}
			}
		}`, text)
	})

	t.Run("running pod without metrics server", func(t *testing.T) {
		tools := newProjectResourceUsageTools(t, fakeToken, fakeURL, "",
			[]runtime.Object{
				cluster,
				project,
				ns1,
				fakeRunningPod("pod-1", "ns-1",
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				),
			},
			nil,
		)

		result, _, err := tools.getProjectResourceUsage(
			middleware.WithToken(t.Context(), fakeToken),
			test.NewCallToolRequest(fakeURL),
			getProjectResourceUsageParams{Name: "my-project", Cluster: "test-cluster"},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		text := result.Content[0].(*mcp.TextContent).Text
		t.Logf("got result: %s", text)
		assert.JSONEq(t, `{
			"llm": {
				"projectResourceUsageSummary": {
					"project": {
						"name": "my-project",
						"displayName": "My Project",
						"cluster": "test-cluster",
						"totals": {
							"podCount": 1,
							"cpu": {"requests": "100m", "limits": "200m", "usage": "0"},
							"memory": {"requests": "128Mi", "limits": "256Mi", "usage": "0"}
						}
					},
					"namespaces": {
						"ns-1": {
							"namespace": "ns-1",
							"cluster": "test-cluster",
							"totals": {
								"podCount": 1,
								"cpu": {"requests": "100m", "limits": "200m", "usage": "0"},
								"memory": {"requests": "128Mi", "limits": "256Mi", "usage": "0"}
							}
						}
					}
				}
			}
		}`, text)
	})

	t.Run("project with no namespaces", func(t *testing.T) {
		tools := newProjectResourceUsageTools(t, fakeToken, fakeURL, "",
			[]runtime.Object{cluster, project},
			nil,
		)

		result, _, err := tools.getProjectResourceUsage(
			middleware.WithToken(t.Context(), fakeToken),
			test.NewCallToolRequest(fakeURL),
			getProjectResourceUsageParams{Name: "my-project", Cluster: "test-cluster"},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		text := result.Content[0].(*mcp.TextContent).Text
		t.Logf("got result: %s", text)
		assert.JSONEq(t, `{
			"llm": {
				"projectResourceUsageSummary": {
					"project": {
						"name": "my-project",
						"displayName": "My Project",
						"cluster": "test-cluster",
						"totals": {
							"podCount": 0,
							"cpu": {"requests": "0", "limits": "0", "usage": "0"},
							"memory": {"requests": "0", "limits": "0", "usage": "0"}
						}
					},
					"namespaces": {}
				}
			}
		}`, text)
	})

	t.Run("non-running pods are skipped", func(t *testing.T) {
		tools := newProjectResourceUsageTools(t, fakeToken, fakeURL, "",
			[]runtime.Object{
				cluster,
				project,
				ns1,
				fakePodWithPhase("pending-pod", "ns-1", corev1.PodPending,
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				),
			},
			nil,
		)

		result, _, err := tools.getProjectResourceUsage(
			middleware.WithToken(t.Context(), fakeToken),
			test.NewCallToolRequest(fakeURL),
			getProjectResourceUsageParams{Name: "my-project", Cluster: "test-cluster"},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		text := result.Content[0].(*mcp.TextContent).Text
		t.Logf("got result: %s", text)
		assert.JSONEq(t, `{
			"llm": {
				"projectResourceUsageSummary": {
					"project": {
						"name": "my-project",
						"displayName": "My Project",
						"cluster": "test-cluster",
						"totals": {
							"podCount": 0,
							"cpu": {"requests": "0", "limits": "0", "usage": "0"},
							"memory": {"requests": "0", "limits": "0", "usage": "0"}
						}
					},
					"namespaces": {
						"ns-1": {
							"namespace": "ns-1",
							"cluster": "test-cluster",
							"totals": {
								"podCount": 0,
								"cpu": {"requests": "0", "limits": "0", "usage": "0"},
								"memory": {"requests": "0", "limits": "0", "usage": "0"}
							}
						}
					}
				}
			}
		}`, text)
	})

	t.Run("init container resources are aggregated", func(t *testing.T) {
		tools := newProjectResourceUsageTools(t, fakeToken, fakeURL, "",
			[]runtime.Object{
				cluster,
				project,
				ns1,
				fakeRunningPodWithInitContainer("pod-with-init", "ns-1",
					// container requests
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
					// container limits
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					// init container requests
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("50m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
					// init container limits
					corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
				),
			},
			nil,
		)

		result, _, err := tools.getProjectResourceUsage(
			middleware.WithToken(t.Context(), fakeToken),
			test.NewCallToolRequest(fakeURL),
			getProjectResourceUsageParams{Name: "my-project", Cluster: "test-cluster"},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)
		text := result.Content[0].(*mcp.TextContent).Text
		t.Logf("got result: %s", text)
		assert.JSONEq(t, `{
			"llm": {
				"projectResourceUsageSummary": {
					"project": {
						"name": "my-project",
						"displayName": "My Project",
						"cluster": "test-cluster",
						"totals": {
							"podCount": 1,
							"cpu": {"requests": "150m", "limits": "300m", "usage": "0"},
							"memory": {"requests": "192Mi", "limits": "384Mi", "usage": "0"}
						}
					},
					"namespaces": {
						"ns-1": {
							"namespace": "ns-1",
							"cluster": "test-cluster",
							"totals": {
								"podCount": 1,
								"cpu": {"requests": "150m", "limits": "300m", "usage": "0"},
								"memory": {"requests": "192Mi", "limits": "384Mi", "usage": "0"}
							}
						}
					}
				}
			}
		}`, text)
	})

	t.Run("project not found", func(t *testing.T) {
		tools := newProjectResourceUsageTools(t, fakeToken, fakeURL, "",
			[]runtime.Object{cluster},
			nil,
		)

		_, _, err := tools.getProjectResourceUsage(
			middleware.WithToken(t.Context(), fakeToken),
			test.NewCallToolRequest(fakeURL),
			getProjectResourceUsageParams{Name: "nonexistent-project", Cluster: "test-cluster"},
		)

		require.Error(t, err)
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("cluster not found", func(t *testing.T) {
		tools := newProjectResourceUsageTools(t, fakeToken, fakeURL, "", nil, nil)

		_, _, err := tools.getProjectResourceUsage(
			middleware.WithToken(t.Context(), fakeToken),
			test.NewCallToolRequest(fakeURL),
			getProjectResourceUsageParams{Name: "my-project", Cluster: "nonexistent-cluster"},
		)

		require.Error(t, err)
		assert.ErrorContains(t, err, "not found")
	})
}

// fakeMgmtCluster creates a fake management.cattle.io/v3 Cluster object.
func fakeMgmtCluster(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Cluster",
			"metadata": map[string]any{
				"name": name,
			},
		},
	}
}

// fakeMgmtProject creates a fake management.cattle.io/v3 Project object.
func fakeMgmtProject(clusterID, name, displayName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Project",
			"metadata": map[string]any{
				"name":      name,
				"namespace": clusterID,
			},
			"spec": map[string]any{
				"displayName": displayName,
			},
		},
	}
}

// fakeProjectNamespace creates a fake Namespace with the projectId label set.
func fakeProjectNamespace(name, projectID string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"field.cattle.io/projectId": projectID,
			},
		},
	}
}

// fakeRunningPod creates a Running Pod with the given resource requests and limits in a single container.
func fakeRunningPod(name, namespace string, requests, limits corev1.ResourceList) *corev1.Pod {
	return fakePodWithPhase(name, namespace, corev1.PodRunning, requests, limits)
}

// fakePodWithPhase creates a Pod in the given phase with a single container.
func fakePodWithPhase(name, namespace string, phase corev1.PodPhase, requests, limits corev1.ResourceList) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "container-1",
					Resources: corev1.ResourceRequirements{
						Requests: requests,
						Limits:   limits,
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: phase,
		},
	}
}

// fakeRunningPodWithInitContainer creates a Running Pod with one regular container
// and one init container, each with the given resource requests and limits.
func fakeRunningPodWithInitContainer(name, namespace string, containerRequests, containerLimits, initRequests, initLimits corev1.ResourceList) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "container-1",
					Resources: corev1.ResourceRequirements{
						Requests: containerRequests,
						Limits:   containerLimits,
					},
				},
			},
			InitContainers: []corev1.Container{
				{
					Name: "init-container-1",
					Resources: corev1.ResourceRequirements{
						Requests: initRequests,
						Limits:   initLimits,
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

// fakePodMetrics creates a PodMetrics object with a single container.
func fakePodMetrics(podName, namespace string, usage corev1.ResourceList) *metricsv1beta1.PodMetrics {
	return &metricsv1beta1.PodMetrics{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "metrics.k8s.io/v1beta1",
			Kind:       "PodMetrics",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Containers: []metricsv1beta1.ContainerMetrics{
			{
				Name:  "container-1",
				Usage: usage,
			},
		},
	}
}
