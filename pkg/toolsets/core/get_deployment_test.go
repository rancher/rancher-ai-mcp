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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
)

var fakeDeployment = &appsv1.Deployment{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "nginx-deployment",
		Namespace: "default",
	},
	Spec: appsv1.DeploymentSpec{
		Replicas: ptr.To(int32(2)),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "nginx",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "nginx",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "nginx",
						Image: "nginx:1.21",
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 80,
								Protocol:      corev1.ProtocolTCP,
							},
						},
					},
				},
			},
		},
	},
}

var fakeDeploymentPod = &corev1.Pod{
	TypeMeta: metav1.TypeMeta{
		Kind:       "Pod",
		APIVersion: "v1",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name:      "nginx-deployment-abc123",
		Namespace: "default",
		Labels: map[string]string{
			"app": "nginx",
		},
	},
	Spec: corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "nginx",
				Image: "nginx:1.21",
			},
		},
	},
	Status: corev1.PodStatus{
		Phase: corev1.PodRunning,
	},
}

func deploymentScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	return scheme
}

func TestGetDeploymentDetails(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params        specificResourceParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"get deployment with pods": {
			params: specificResourceParams{
				Name:      "nginx-deployment",
				Namespace: "default",
				Cluster:   "local",
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(deploymentScheme(), map[schema.GroupVersionResource]string{
				{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList",
				{Group: "", Version: "v1", Resource: "pods"}:            "PodList",
			}, fakeDeployment, fakeDeploymentPod),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "apps/v1",
						"kind": "Deployment",
						"metadata": {"name": "nginx-deployment", "namespace": "default"},
						"spec": {
							"replicas": 2,
							"selector": {"matchLabels": {"app": "nginx"}},
							"strategy": {},
							"template": {
								"metadata": {"labels": {"app": "nginx"}},
								"spec": {
									"containers": [
										{"image": "nginx:1.21", "name": "nginx", "ports": [{"containerPort": 80, "protocol": "TCP"}], "resources": {}}
									]
								}
							}
						},
						"status": {}
					},
					{
						"apiVersion": "v1",
						"kind": "Pod",
						"metadata": {"labels": {"app": "nginx"}, "name": "nginx-deployment-abc123", "namespace": "default"},
						"spec": {"containers": [{"image": "nginx:1.21", "name": "nginx", "resources": {}}]},
						"status": {"phase": "Running"}
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "Deployment", "name": "nginx-deployment", "namespace": "default", "type": "apps.deployment"},
					{"cluster": "local", "kind": "Pod", "name": "nginx-deployment-abc123", "namespace": "default", "type": "pod"}
				]
			}`,
		},
		"get deployment - not found": {
			params: specificResourceParams{
				Name:      "nonexistent-deployment",
				Namespace: "default",
				Cluster:   "local",
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(deploymentScheme(), map[schema.GroupVersionResource]string{
				{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList",
				{Group: "", Version: "v1", Resource: "pods"}:            "PodList",
			}),
			expectedError: `deployments.apps "nonexistent-deployment" not found`,
		},
		"get deployment when tool is configured with URL": {
			params: specificResourceParams{
				Name:      "nginx-deployment",
				Namespace: "default",
				Cluster:   "local",
			},
			rancherURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(deploymentScheme(), map[schema.GroupVersionResource]string{
				{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList",
				{Group: "", Version: "v1", Resource: "pods"}:            "PodList",
			}, fakeDeployment, fakeDeploymentPod),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "apps/v1",
						"kind": "Deployment",
						"metadata": {"name": "nginx-deployment", "namespace": "default"},
						"spec": {
							"replicas": 2,
							"selector": {"matchLabels": {"app": "nginx"}},
							"strategy": {},
							"template": {
								"metadata": {"labels": {"app": "nginx"}},
								"spec": {
									"containers": [
										{"image": "nginx:1.21", "name": "nginx", "ports": [{"containerPort": 80, "protocol": "TCP"}], "resources": {}}
									]
								}
							}
						},
						"status": {}
					},
					{
						"apiVersion": "v1",
						"kind": "Pod",
						"metadata": {"labels": {"app": "nginx"}, "name": "nginx-deployment-abc123", "namespace": "default"},
						"spec": {"containers": [{"image": "nginx:1.21", "name": "nginx", "resources": {}}]},
						"status": {"phase": "Running"}
					}
				],
				"uiContext": [
					{"cluster": "local", "kind": "Deployment", "name": "nginx-deployment", "namespace": "default", "type": "apps.deployment"},
					{"cluster": "local", "kind": "Pod", "name": "nginx-deployment-abc123", "namespace": "default", "type": "pod"}
				]
			}`,
		},
		"get deployment from cluster - no rancherURL or request URL": {
			params: specificResourceParams{
				Name:      "nonexistent-deployment",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(deploymentScheme(), map[schema.GroupVersionResource]string{
				{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList",
				{Group: "", Version: "v1", Resource: "pods"}:            "PodList",
			}),
			expectedError: "no URL for rancher request",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return tt.fakeDynClient, nil
				},
			}
			tools := NewTools(test.WrapClient(c, fakeToken, fakeUrl), tt.rancherURL)
			req := test.NewCallToolRequest(tt.requestURL)

			result, _, err := tools.getDeploymentDetails(middleware.WithToken(t.Context(), fakeToken), req, tt.params)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
