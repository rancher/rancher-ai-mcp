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

var fakePodWithImage = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-pod",
		Namespace: "default",
	},
	Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{
			{
				Name:  "init-container",
				Image: "busybox:latest",
			},
		},
		Containers: []corev1.Container{
			{
				Name:  "app-container",
				Image: "nginx:1.21",
			},
			{
				Name:  "sidecar-container",
				Image: "redis:alpine",
			},
		},
	},
}

func podScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func TestGetClusterImages(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params        getClusterImagesParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL string

		expectedResult string
		expectedError  string
	}{
		"get images from single cluster": {
			params:     getClusterImagesParams{Clusters: []string{"local"}},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(podScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "pods"}: "PodList",
			}, fakePodWithImage),
			expectedResult: `{
				"local": ["busybox:latest", "nginx:1.21", "redis:alpine"]
			}`,
		},
		"get images from cluster with no pods": {
			params:     getClusterImagesParams{Clusters: []string{"local"}},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(podScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "pods"}: "PodList",
			}),
			expectedResult: `{
				"local": []
			}`,
		},
		"get images from cluster when tool is configured with URL": {
			params: getClusterImagesParams{Clusters: []string{"local"}},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(podScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "pods"}: "PodList",
			}, fakePodWithImage),
			rancherURL: fakeUrl,
			expectedResult: `{
				"local": ["busybox:latest", "nginx:1.21", "redis:alpine"]
			}`,
		},
		"get images from cluster - no rancherURL or request URL": {
			// fails because requestURL and rancherURL are not configured (no
			// R_Url or configured Rancher URL.
			params: getClusterImagesParams{Clusters: []string{"local"}},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(podScheme(), map[schema.GroupVersionResource]string{
				{Group: "", Version: "v1", Resource: "pods"}: "PodList",
			}, fakePodWithImage),
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

			result, _, err := tools.getClusterImages(middleware.WithToken(t.Context(), fakeToken), req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
