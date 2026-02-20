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

var fakeGitRepo1 = &unstructured.Unstructured{
	Object: map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1",
		"kind":       "GitRepo",
		"metadata": map[string]any{
			"name":      "gitrepo-1",
			"namespace": "fleet-default",
		},
		"spec": map[string]any{
			"repo": "https://github.com/example/repo1",
			"paths": []any{
				"charts/",
			},
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

var fakeGitRepo2 = &unstructured.Unstructured{
	Object: map[string]any{
		"apiVersion": "fleet.cattle.io/v1alpha1",
		"kind":       "GitRepo",
		"metadata": map[string]any{
			"name":      "gitrepo-2",
			"namespace": "fleet-default",
		},
		"spec": map[string]any{
			"repo": "https://github.com/example/repo2",
			"paths": []any{
				"manifests/",
			},
			"targets": []any{
				map[string]any{
					"clusterName": "cluster-2",
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

func listGitReposScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	return scheme
}

func TestListGitRepos(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params        listGitRepoParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		// used in the CallToolRequest
		requestURL string
		// used in the creation of the Tools.
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"list gitrepos in workspace": {
			params: listGitRepoParams{
				Workspace: "fleet-default",
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(listGitReposScheme(), map[schema.GroupVersionResource]string{
				{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "gitrepos"}: "GitRepoList",
			}, fakeGitRepo1, fakeGitRepo2),
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "fleet.cattle.io/v1alpha1",
						"kind": "GitRepo",
						"metadata": {"name": "gitrepo-1", "namespace": "fleet-default"},
						"spec": {
							"repo": "https://github.com/example/repo1",
							"paths": ["charts/"],
							"targets": [{"clusterName": "cluster-1"}]
						},
						"status": {
							"conditions": [{"type": "Ready", "status": "True"}]
						}
					},
					{
						"apiVersion": "fleet.cattle.io/v1alpha1",
						"kind": "GitRepo",
						"metadata": {"name": "gitrepo-2", "namespace": "fleet-default"},
						"spec": {
							"repo": "https://github.com/example/repo2",
							"paths": ["manifests/"],
							"targets": [{"clusterName": "cluster-2"}]
						},
						"status": {
							"conditions": [{"type": "Ready", "status": "True"}]
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "GitRepo",
						"name": "gitrepo-1",
						"namespace": "fleet-default",
						"type": "fleet.cattle.io.gitrepo"
					},
					{
						"cluster": "local",
						"kind": "GitRepo",
						"name": "gitrepo-2",
						"namespace": "fleet-default",
						"type": "fleet.cattle.io.gitrepo"
					}
				]
			}`,
		},
		"list gitrepos - empty workspace": {
			params: listGitRepoParams{
				Workspace: "empty-workspace",
			},
			requestURL: fakeUrl,
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(listGitReposScheme(), map[schema.GroupVersionResource]string{
				{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "gitrepos"}: "GitRepoList",
			}),
			expectedResult: `{"llm": "no resources found"}`,
		},
		"list repos when tool is configured with URL": {
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(listGitReposScheme(), map[schema.GroupVersionResource]string{
				{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "gitrepos"}: "GitRepoList",
			}, fakeGitRepo1, fakeGitRepo2),
			rancherURL: fakeUrl,
			expectedResult: `{
				"llm": [
					{
						"apiVersion": "fleet.cattle.io/v1alpha1",
						"kind": "GitRepo",
						"metadata": {"name": "gitrepo-1", "namespace": "fleet-default"},
						"spec": {
							"repo": "https://github.com/example/repo1",
							"paths": ["charts/"],
							"targets": [{"clusterName": "cluster-1"}]
						},
						"status": {
							"conditions": [{"type": "Ready", "status": "True"}]
						}
					},
					{
						"apiVersion": "fleet.cattle.io/v1alpha1",
						"kind": "GitRepo",
						"metadata": {"name": "gitrepo-2", "namespace": "fleet-default"},
						"spec": {
							"repo": "https://github.com/example/repo2",
							"paths": ["manifests/"],
							"targets": [{"clusterName": "cluster-2"}]
						},
						"status": {
							"conditions": [{"type": "Ready", "status": "True"}]
						}
					}
				],
				"uiContext": [
					{
						"cluster": "local",
						"kind": "GitRepo",
						"name": "gitrepo-1",
						"namespace": "fleet-default",
						"type": "fleet.cattle.io.gitrepo"
					},
					{
						"cluster": "local",
						"kind": "GitRepo",
						"name": "gitrepo-2",
						"namespace": "fleet-default",
						"type": "fleet.cattle.io.gitrepo"
					}
				]
			}`,
		},
		"list gitrepos - no rancherURL or request URL": {
			params: listGitRepoParams{
				Workspace: "empty-workspace",
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(listGitReposScheme(), map[schema.GroupVersionResource]string{
				{Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "gitrepos"}: "GitRepoList",
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

			result, _, err := tools.listGitRepos(
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
