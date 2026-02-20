package core

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
)

var fakePodForInspect = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "nginx-pod-abc123",
		Namespace: "default",
		OwnerReferences: []metav1.OwnerReference{
			{
				APIVersion: "apps/v1",
				Kind:       "ReplicaSet",
				Name:       "nginx-replicaset",
				Controller: ptr.To(true),
			},
		},
	},
	Spec: corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "nginx",
				Image: "nginx:1.21",
			},
			{
				Name:  "sidecar",
				Image: "busybox:latest",
			},
		},
	},
	Status: corev1.PodStatus{
		Phase: corev1.PodRunning,
	},
}

var fakeReplicaSet = &appsv1.ReplicaSet{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "nginx-replicaset",
		Namespace: "default",
		OwnerReferences: []metav1.OwnerReference{
			{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "nginx-deployment",
				Controller: ptr.To(true),
			},
		},
	},
	Spec: appsv1.ReplicaSetSpec{
		Replicas: ptr.To(int32(1)),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "nginx",
			},
		},
	},
}

var fakeDeploymentForInspect = &appsv1.Deployment{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "nginx-deployment",
		Namespace: "default",
	},
	Spec: appsv1.DeploymentSpec{
		Replicas: ptr.To(int32(1)),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "nginx",
			},
		},
	},
}

func inspectPodScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	return scheme
}

func TestInspectPod(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params specificResourceParams
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		fakeClientset  *fake.Clientset
		fakeDynClient  *dynamicfake.FakeDynamicClient
		expectedError  string
		expectedResult string
	}{
		"inspect pod": {
			params: specificResourceParams{
				Name:      "nginx-pod-abc123",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeClientset: fake.NewSimpleClientset(fakeDeploymentForInspect, fakeReplicaSet, fakePodForInspect),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(inspectPodScheme(), fakePodForInspect, fakeReplicaSet, fakeDeploymentForInspect),
			requestURL:    fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "v1",
						"kind": "Pod",
						"metadata": {
							"name": "nginx-pod-abc123",
							"namespace": "default",
							"ownerReferences": [
								{
									"apiVersion": "apps/v1",
									"controller": true,
									"kind": "ReplicaSet",
									"name": "nginx-replicaset",
									"uid": ""
								}
							]
						},
						"spec": {
							"containers": [
								{
									"image": "nginx:1.21",
									"name": "nginx",
									"resources": {}
								},
								{
									"image": "busybox:latest",
									"name": "sidecar",
									"resources": {}
								}
							]
						},
						"status": {
							"phase": "Running"
						}
					},
					{
						"apiVersion": "apps/v1",
						"kind": "Deployment",
						"metadata": {
							"name": "nginx-deployment",
							"namespace": "default"
						},
						"spec": {
							"replicas": 1,
							"selector": {
								"matchLabels": {
									"app": "nginx"
								}
							},
							"strategy": {},
							"template": {
								"metadata": {},
								"spec": {
									"containers": null
								}
							}
						},
						"status": {}
					},
					{
						"pod-logs": {
							"nginx": "fake logs",
							"sidecar": "fake logs"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Pod",
						"name": "nginx-pod-abc123",
						"namespace": "default",
						"type": "pod"
					},
					{
						"cluster": "local",
						"kind": "Deployment",
						"name": "nginx-deployment",
						"namespace": "default",
						"type": "apps.deployment"
					}
				]
			}`,
		},
		"inspect pod when tool is configured with URL": {
			params: specificResourceParams{
				Name:      "nginx-pod-abc123",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeClientset: fake.NewSimpleClientset(fakeDeploymentForInspect, fakeReplicaSet, fakePodForInspect),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(inspectPodScheme(), fakePodForInspect, fakeReplicaSet, fakeDeploymentForInspect),
			rancherURL:    fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "v1",
						"kind": "Pod",
						"metadata": {
							"name": "nginx-pod-abc123",
							"namespace": "default",
							"ownerReferences": [
								{
									"apiVersion": "apps/v1",
									"controller": true,
									"kind": "ReplicaSet",
									"name": "nginx-replicaset",
									"uid": ""
								}
							]
						},
						"spec": {
							"containers": [
								{
									"image": "nginx:1.21",
									"name": "nginx",
									"resources": {}
								},
								{
									"image": "busybox:latest",
									"name": "sidecar",
									"resources": {}
								}
							]
						},
						"status": {
							"phase": "Running"
						}
					},
					{
						"apiVersion": "apps/v1",
						"kind": "Deployment",
						"metadata": {
							"name": "nginx-deployment",
							"namespace": "default"
						},
						"spec": {
							"replicas": 1,
							"selector": {
								"matchLabels": {
									"app": "nginx"
								}
							},
							"strategy": {},
							"template": {
								"metadata": {},
								"spec": {
									"containers": null
								}
							}
						},
						"status": {}
					},
					{
						"pod-logs": {
							"nginx": "fake logs",
							"sidecar": "fake logs"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Pod",
						"name": "nginx-pod-abc123",
						"namespace": "default",
						"type": "pod"
					},
					{
						"cluster": "local",
						"kind": "Deployment",
						"name": "nginx-deployment",
						"namespace": "default",
						"type": "apps.deployment"
					}
				]
			}`,
		},

		"inspect pod - not found": {
			params: specificResourceParams{
				Name:      "nonexistent-pod",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeClientset: fake.NewSimpleClientset(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(inspectPodScheme()),
			requestURL:    fakeUrl,
			expectedError: `pods "nonexistent-pod" not found`,
		},
		"inspect pod - statefulset parent": {
			params: specificResourceParams{
				Name:      "stateful-pod-abc",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeClientset: fake.NewSimpleClientset(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(inspectPodScheme(),
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stateful-pod-abc",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "stateful-rs",
							Controller: ptr.To(true),
						}},
					},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:latest"}}},
				},
				&appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stateful-rs",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion: "apps/v1",
							Kind:       "StatefulSet",
							Name:       "my-statefulset",
							Controller: ptr.To(true),
						}},
					},
				},
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-statefulset",
						Namespace: "default",
					},
				},
			),
			requestURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "v1",
						"kind": "Pod",
						"metadata": {
							"name": "stateful-pod-abc",
							"namespace": "default",
							"ownerReferences": [
								{
									"apiVersion": "apps/v1",
									"controller": true,
									"kind": "ReplicaSet",
									"name": "stateful-rs",
									"uid": ""
								}
							]
						},
						"spec": {
							"containers": [
								{
									"image": "app:latest",
									"name": "app",
									"resources": {}
								}
							]
						},
						"status": {}
					},
					{
						"apiVersion": "apps/v1",
						"kind": "StatefulSet",
						"metadata": {
							"name": "my-statefulset",
							"namespace": "default"
						},
						"spec": {
							"selector": null,
							"serviceName": "",
							"template": {
								"metadata": {},
								"spec": {
									"containers": null
								}
							},
							"updateStrategy": {}
						},
						"status": {
							"availableReplicas": 0,
							"replicas": 0
						}
					},
					{
						"pod-logs": {
							"app": "fake logs"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Pod",
						"name": "stateful-pod-abc",
						"namespace": "default",
						"type": "pod"
					},
					{
						"cluster": "local",
						"kind": "StatefulSet",
						"name": "my-statefulset",
						"namespace": "default",
						"type": "apps.statefulset"
					}
				]
			}`,
		},
		"inspect pod - daemonset parent": {
			params: specificResourceParams{
				Name:      "daemon-pod-xyz",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeClientset: fake.NewSimpleClientset(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(inspectPodScheme(),
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemon-pod-xyz",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "daemon-rs",
							Controller: ptr.To(true),
						}},
					},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "daemon", Image: "daemon:latest"}}},
				},
				&appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemon-rs",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion: "apps/v1",
							Kind:       "DaemonSet",
							Name:       "my-daemonset",
							Controller: ptr.To(true),
						}},
					},
				},
				&appsv1.DaemonSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-daemonset",
						Namespace: "default",
					},
				},
			),
			requestURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "v1",
						"kind": "Pod",
						"metadata": {
							"name": "daemon-pod-xyz",
							"namespace": "default",
							"ownerReferences": [
								{
									"apiVersion": "apps/v1",
									"controller": true,
									"kind": "ReplicaSet",
									"name": "daemon-rs",
									"uid": ""
								}
							]
						},
						"spec": {
							"containers": [
								{
									"image": "daemon:latest",
									"name": "daemon",
									"resources": {}
								}
							]
						},
						"status": {}
					},
					{
						"apiVersion": "apps/v1",
						"kind": "DaemonSet",
						"metadata": {
							"name": "my-daemonset",
							"namespace": "default"
						},
						"spec": {
							"selector": null,
							"template": {
								"metadata": {},
								"spec": {
									"containers": null
								}
							},
							"updateStrategy": {}
						},
						"status": {
							"currentNumberScheduled": 0,
							"desiredNumberScheduled": 0,
							"numberMisscheduled": 0,
							"numberReady": 0
						}
					},
					{
						"pod-logs": {
							"daemon": "fake logs"
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Pod",
						"name": "daemon-pod-xyz",
						"namespace": "default",
						"type": "pod"
					},
					{
						"cluster": "local",
						"kind": "DaemonSet",
						"name": "my-daemonset",
						"namespace": "default",
						"type": "apps.daemonset"
					}
				]
			}`,
		},
		"inspect pod - no rancherURL or request URL": {
			params: specificResourceParams{
				Name:      "nonexistent-pod",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeClientset: fake.NewSimpleClientset(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(inspectPodScheme()),
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
			tools := NewTools(test.WrapClient(c, fakeToken, fakeUrl), tt.rancherURL)
			req := test.NewCallToolRequest(tt.requestURL)

			result, _, err := tools.inspectPod(middleware.WithToken(t.Context(), fakeToken), req, tt.params)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
