package fleet

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

var fakeBundle1 = &unstructured.Unstructured{
	Object: map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1",
		"kind":       "Bundle",
		"metadata": map[string]any{
			"name":      "bundle-1",
			"namespace": "fleet-default",
		},
		"spec": map[string]any{
			"targets": []any{
				map[string]any{
					"clusterName": "cluster-1",
				},
			},
		},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{
					"type":   "Ready",
					"status": "True",
				},
			},
		},
	},
}

func bundleScheme() *runtime.Scheme {
	return runtime.NewScheme()
}

func TestGetBundle(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params        getBundleParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"get bundle by name": {
			params: getBundleParams{
				Name:      "bundle-1",
				Workspace: "fleet-default",
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(bundleScheme(), map[schema.GroupVersionResource]string{
				{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundles"}: "BundleList",
			}, fakeBundle1),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "fleet.cattle.io/v1alpha1",
						"kind": "Bundle",
						"metadata": {"name": "bundle-1", "namespace": "fleet-default"},
						"spec": {
							"targets": [{"clusterName": "cluster-1"}]
						},
						"status": {
							"conditions": [{"type": "Ready", "status": "True"}]
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Bundle",
						"name": "bundle-1",
						"namespace": "fleet-default",
						"type": "fleet.cattle.io.bundle"
					}
				]
			}`,
		},
		"get bundle when tool is configured with URL": {
			params: getBundleParams{
				Name:      "bundle-1",
				Workspace: "fleet-default",
			},
			rancherURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(bundleScheme(), map[schema.GroupVersionResource]string{
				{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundles"}: "BundleList",
			}, fakeBundle1),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "fleet.cattle.io/v1alpha1",
						"kind": "Bundle",
						"metadata": {"name": "bundle-1", "namespace": "fleet-default"},
						"spec": {
							"targets": [{"clusterName": "cluster-1"}]
						},
						"status": {
							"conditions": [{"type": "Ready", "status": "True"}]
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "Bundle",
						"name": "bundle-1",
						"namespace": "fleet-default",
						"type": "fleet.cattle.io.bundle"
					}
				]
			}`,
		},
		"get bundle - not found": {
			params: getBundleParams{
				Name:      "nonexistent-bundle",
				Workspace: "fleet-default",
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(bundleScheme(), map[schema.GroupVersionResource]string{
				{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundles"}: "BundleList",
			}),
			expectedError: `nonexistent-bundle" not found`,
		},
		"get bundle - no rancherURL or request URL": {
			params: getBundleParams{
				Name:      "bundle-1",
				Workspace: "fleet-default",
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(bundleScheme(), map[schema.GroupVersionResource]string{
				{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundles"}: "BundleList",
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

			result, _, err := tools.getBundle(
				middleware.WithToken(t.Context(), fakeToken),
				req, tt.params)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
