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
)

func patchResourcePlanScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	return scheme
}

func TestUpdateKubernetesResourcePlan(t *testing.T) {
	tests := map[string]struct {
		params         updateKubernetesResourceParams
		expectedResult string
		expectedError  string
	}{
		"update configmap plan - add new key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "ConfigMap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "add",
						Path:  "/data/key3",
						Value: "value3",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": {
					"original": {"apiVersion":"v1","data":{"key1":"value1","key2":"value2"},"kind":"ConfigMap","metadata":{"name":"test-config","namespace":"default"}},
					"patch": [{"op": "add", "path": "/data/key3", "value": "value3"}],
					"patched": {"apiVersion":"v1","data":{"key1":"value1","key2":"value2","key3":"value3"},"kind":"ConfigMap","metadata":{"name":"test-config","namespace":"default"}}
				},
				"resource": {
					"name": "test-config",
					"kind": "ConfigMap",
					"cluster": "local",
					"namespace": "default"
				}
			}]`,
		},
		"update configmap plan - replace existing key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "ConfigMap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "replace",
						Path:  "/data/key1",
						Value: "updated-value",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": {
					"original": {"apiVersion":"v1","data":{"key1":"value1","key2":"value2"},"kind":"ConfigMap","metadata":{"name":"test-config","namespace":"default"}},
					"patch": [{"op": "replace", "path": "/data/key1", "value": "updated-value"}],
					"patched": {"apiVersion":"v1","data":{"key1":"updated-value","key2":"value2"},"kind":"ConfigMap","metadata":{"name":"test-config","namespace":"default"}}
				},
				"resource": {
					"name": "test-config",
					"kind": "ConfigMap",
					"cluster": "local",
					"namespace": "default"
				}
			}]`,
		},
		"update configmap plan - remove key": {
			params: updateKubernetesResourceParams{
				Name:      "test-config",
				Namespace: "default",
				Kind:      "ConfigMap",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:   "remove",
						Path: "/data/key2",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": {
					"original": {"apiVersion":"v1","data":{"key1":"value1","key2":"value2"},"kind":"ConfigMap","metadata":{"name":"test-config","namespace":"default"}},
					"patch": [{"op": "remove", "path": "/data/key2"}],
					"patched": {"apiVersion":"v1","data":{"key1":"value1"},"kind":"ConfigMap","metadata":{"name":"test-config","namespace":"default"}}
				},
				"resource": {
					"name": "test-config",
					"kind": "ConfigMap",
					"cluster": "local",
					"namespace": "default"
				}
			}]`,
		},
		"update plan - multiple patches": {
			params: updateKubernetesResourceParams{
				Name:      "my-deploy",
				Namespace: "staging",
				Kind:      "Deployment",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "replace",
						Path:  "/spec/replicas",
						Value: 3,
					},
					{
						Op:    "add",
						Path:  "/metadata/labels/env",
						Value: "staging",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": {
					"original": {"apiVersion":"apps/v1","kind":"Deployment","metadata":{"labels":{"existing":"label"},"name":"my-deploy","namespace":"staging"},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"myapp"}},"strategy":{},"template":{"metadata":{},"spec":{"containers":null}}},"status":{}},
					"patch": [
						{"op": "replace", "path": "/spec/replicas", "value": 3},
						{"op": "add", "path": "/metadata/labels/env", "value": "staging"}
					],
					"patched": {"apiVersion":"apps/v1","kind":"Deployment","metadata":{"labels":{"env":"staging","existing":"label"},"name":"my-deploy","namespace":"staging"},"spec":{"replicas":3,"selector":{"matchLabels":{"app":"myapp"}},"strategy":{},"template":{"metadata":{},"spec":{"containers":null}}},"status":{}}
				},
				"resource": {
					"name": "my-deploy",
					"kind": "Deployment",
					"cluster": "local",
					"namespace": "staging"
				}
			}]`,
		},
		"update plan - cluster-scoped resource": {
			params: updateKubernetesResourceParams{
				Name:      "my-ns",
				Namespace: "",
				Kind:      "Namespace",
				Cluster:   "local",
				Patch: []jsonPatch{
					{
						Op:    "add",
						Path:  "/metadata/labels/team",
						Value: "platform",
					},
				},
			},
			expectedResult: `[{
				"type": "update",
				"payload": {
					"original": {"apiVersion":"v1","kind":"Namespace","metadata":{"labels":{"existing":"label"},"name":"my-ns"},"spec":{},"status":{}},
					"patch": [{"op": "add", "path": "/metadata/labels/team", "value": "platform"}],
					"patched": {"apiVersion":"v1","kind":"Namespace","metadata":{"labels":{"existing":"label","team":"platform"},"name":"my-ns"},"spec":{},"status":{}}
				},
				"resource": {
					"name": "my-ns",
					"kind": "Namespace",
					"cluster": "local",
					"namespace": ""
				}
			}]`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var fakeClientset *fake.Clientset
			var fakeDynClient *dynamicfake.FakeDynamicClient

			// Create different mock resources based on the test kind
			if tt.params.Kind == "ConfigMap" {
				fakeConfigMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-config",
						Namespace: "default",
					},
					Data: map[string]string{
						"key1": "value1",
						"key2": "value2",
					},
				}
				fakeClientset = fake.NewSimpleClientset(fakeConfigMap)
				fakeDynClient = dynamicfake.NewSimpleDynamicClient(patchResourcePlanScheme(), fakeConfigMap)
			} else if tt.params.Kind == "Deployment" {
				fakeDeployment := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-deploy",
						Namespace: "staging",
						Labels:    map[string]string{"existing": "label"},
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: &[]int32{1}[0],
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "myapp",
							},
						},
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{},
						},
					},
				}
				fakeClientset = fake.NewSimpleClientset(fakeDeployment)
				fakeDynClient = dynamicfake.NewSimpleDynamicClient(patchResourcePlanScheme(), fakeDeployment)
			} else if tt.params.Kind == "Namespace" {
				fakeNamespace := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "my-ns",
						Labels: map[string]string{"existing": "label"},
					},
				}
				fakeClientset = fake.NewSimpleClientset(fakeNamespace)
				fakeDynClient = dynamicfake.NewSimpleDynamicClient(patchResourcePlanScheme(), fakeNamespace)
			}

			c := &client.Client{
				ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
					return fakeClientset, nil
				},
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return fakeDynClient, nil
				},
			}

			tools := NewTools(test.WrapClient(c, "test-token"), false)
			req := test.NewCallToolRequest("https://localhost:8080")
			ctx := middleware.WithToken(t.Context(), "test-token")

			result, _, err := tools.updateKubernetesResourcePlan(ctx, req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
