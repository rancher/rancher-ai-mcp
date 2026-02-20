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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

var fakePod1 = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "pod-1",
		Namespace: "default",
	},
	Spec: corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "nginx",
				Image: "nginx:latest",
			},
		},
	},
	Status: corev1.PodStatus{
		Phase: corev1.PodRunning,
	},
}

var fakePod2 = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "pod-2",
		Namespace: "default",
	},
	Spec: corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "redis",
				Image: "redis:latest",
			},
		},
	},
	Status: corev1.PodStatus{
		Phase: corev1.PodRunning,
	},
}

func listResourcesScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func TestListKubernetesResources(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params        listKubernetesResourcesParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"list pods in namespace": {
			params: listKubernetesResourcesParams{
				Kind:      "pod",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(listResourcesScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "pods"}: "PodList",
			}, fakePod1, fakePod2),
			requestURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"metadata": {"name": "pod-1", "namespace": "default"},
						"spec": {"containers": [{"image": "nginx:latest", "name": "nginx", "resources": {}}]},
						"status": {"phase": "Running"}
					},
					{
						"metadata": {"name": "pod-2", "namespace": "default"},
						"spec": {"containers": [{"image": "redis:latest", "name": "redis", "resources": {}}]},
						"status": {"phase": "Running"}
					}
				]
			}`,
		},
		"list pods - empty namespace": {
			params: listKubernetesResourcesParams{
				Kind:      "pod",
				Namespace: "kube-system",
				Cluster:   "local",
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(listResourcesScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "pods"}: "PodList",
			}),
			expectedResult: `{"llm": "no resources found"}`,
		},
		"list pods - when tool is configured with URL": {
			params: listKubernetesResourcesParams{
				Kind:      "pod",
				Namespace: "default",
				Cluster:   "local",
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(listResourcesScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "pods"}: "PodList",
			}, fakePod1, fakePod2),
			rancherURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"metadata": {"name": "pod-1", "namespace": "default"},
						"spec": {"containers": [{"image": "nginx:latest", "name": "nginx", "resources": {}}]},
						"status": {"phase": "Running"}
					},
					{
						"metadata": {"name": "pod-2", "namespace": "default"},
						"spec": {"containers": [{"image": "redis:latest", "name": "redis", "resources": {}}]},
						"status": {"phase": "Running"}
					}
				]
			}`,
		},
		"list pods - no rancherURL or request URL": {
			params: listKubernetesResourcesParams{
				Kind:      "pod",
				Namespace: "kube-system",
				Cluster:   "local",
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(listResourcesScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "pods"}: "PodList",
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

			result, _, err := tools.listKubernetesResources(middleware.WithToken(t.Context(), fakeToken), req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
