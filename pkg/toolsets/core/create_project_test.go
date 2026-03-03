package core

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

func createProjectScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	return scheme
}

func TestCreateProject(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params        createProjectParams
		fakeDynClient *dynamicfake.FakeDynamicClient

		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"create project": {
			params: createProjectParams{
				Cluster:     "local",
				Name:        "test-project",
				DisplayName: "Test Project",
				Description: "A test project",
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(createProjectScheme(), map[schema.GroupVersionResource]string{
				{Group: "management.cattle.io", Version: "v3", Resource: "projects"}: "ProjectList",
			}),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Project",
						"metadata": {"name": "test-project", "namespace": "local"},
						"spec": {
							"clusterName": "local",
							"displayName": "Test Project",
							"description": "A test project",
							"containerDefaultResourceLimit": {}
						}
					}
				],
				"uiContext": [
					{"namespace": "local", "kind": "Project", "cluster": "local", "name": "test-project", "type": "project"}
				]
			}`,
		},
		"create project with resource limits": {
			params: createProjectParams{
				Cluster:           "local",
				Name:              "project-with-limits",
				DisplayName:       "Project with Limits",
				CPULimit:          2000,
				CPUReservation:    1000,
				MemoryLimit:       4096,
				MemoryReservation: 2048,
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(createProjectScheme(), map[schema.GroupVersionResource]string{
				{Group: "management.cattle.io", Version: "v3", Resource: "projects"}: "ProjectList",
			}),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Project",
						"metadata": {"name": "project-with-limits", "namespace": "local"},
						"spec": {
							"clusterName": "local",
							"displayName": "Project with Limits",
							"containerDefaultResourceLimit": {
								"limitsCpu": "2000m",
								"requestsCpu": "1000m",
								"limitsMemory": "4096Mi",
								"requestsMemory": "2048Mi"
							}
						}
					}
				],
				"uiContext": [
					{"namespace": "local", "kind": "Project", "cluster": "local", "name": "project-with-limits", "type": "project"}
				]
			}`,
		},
		"create project when tool is configured with URL": {
			params: createProjectParams{
				Cluster: "local",
				Name:    "configured-url-project",
			},
			rancherURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(createProjectScheme(), map[schema.GroupVersionResource]string{
				{Group: "management.cattle.io", Version: "v3", Resource: "projects"}: "ProjectList",
			}),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "management.cattle.io/v3",
						"kind": "Project",
						"metadata": {"name": "configured-url-project", "namespace": "local"},
						"spec": {
							"clusterName": "local",
							"containerDefaultResourceLimit": {}
						}
					}
				],
				"uiContext": [
					{"namespace": "local", "kind": "Project", "cluster": "local", "name": "configured-url-project", "type": "project"}
				]
			}`,
		},
		"create project - no rancherURL or request URL": {
			// fails because requestURL and rancherURL are not configured (no
			// R_Url or configured Rancher URL.
			params: createProjectParams{
				Cluster: "local",
				Name:    "error-project",
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(createProjectScheme()),
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

			result, _, err := tools.createProject(middleware.WithToken(t.Context(), fakeToken), req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
			}
		})
	}
}
