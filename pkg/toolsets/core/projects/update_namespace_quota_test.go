package projects

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
)

func namespaceQuotaTestProject() *unstructured.Unstructured {
	return quotaTestProject(map[string]any{
		"resourceQuota": map[string]any{"limit": map[string]any{
			"limitsCpu": "10", "extended": map[string]any{"requests.nvidia.com/gpu": "1"},
		}},
		"namespaceDefaultResourceQuota": map[string]any{"limit": map[string]any{
			"limitsCpu": "4", "extended": map[string]any{"requests.nvidia.com/gpu": "0"},
		}},
	})
}

func namespaceWithQuota(name, annotation string) *unstructured.Unstructured {
	ns := quotaTestNamespace(name)
	if annotation != "" {
		ns.SetAnnotations(map[string]string{namespaceQuotaAnnotation: annotation})
	}

	return ns
}

func TestUpdateNamespaceQuota(t *testing.T) {
	tools, _ := quotaTestTools(namespaceQuotaTestProject(), namespaceWithQuota("web-frontend", ""), namespaceWithQuota("web-api", ""))
	ctx := middleware.WithToken(t.Context(), quotaToken)

	_, _, err := tools.updateNamespaceQuota(ctx, &mcp.CallToolRequest{}, updateNamespaceQuotaParams{
		Cluster: quotaClusterID, Namespace: "web-frontend", Quota: map[string]string{"limitsCpu": "6", "nvidia.com/gpu": "1"},
	})
	require.NoError(t, err)

	updated, err := tools.client.GetResource(ctx, client.GetParams{Cluster: quotaClusterID, Kind: "namespace", Name: "web-frontend", Token: quotaToken})
	require.NoError(t, err)

	var annotation struct {
		Limit map[string]any `json:"limit"`
	}
	require.NoError(t, json.Unmarshal([]byte(updated.GetAnnotations()[namespaceQuotaAnnotation]), &annotation))
	assert.Equal(t, map[string]string{"limitsCpu": "6", "requests.nvidia.com/gpu": "1"}, flattenLimit(annotation.Limit))
}

func TestUpdateNamespaceQuotaPlan(t *testing.T) {
	tools, _ := quotaTestTools(namespaceQuotaTestProject(), namespaceWithQuota("web-frontend", ""))
	ctx := middleware.WithToken(t.Context(), quotaToken)

	result, _, err := tools.updateNamespaceQuotaPlan(ctx, &mcp.CallToolRequest{}, updateNamespaceQuotaParams{
		Cluster: quotaClusterID, Namespace: "web-frontend", Quota: map[string]string{"limitsCpu": "6"},
	})
	require.NoError(t, err)

	var plan []map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &plan))
	require.Len(t, plan, 1)
	assert.Equal(t, "update", plan[0]["type"])
	assert.Equal(t, "Namespace", plan[0]["resource"].(map[string]any)["kind"])

	// Requesting the default the namespace already has reports no changes.
	result, _, err = tools.updateNamespaceQuotaPlan(ctx, &mcp.CallToolRequest{}, updateNamespaceQuotaParams{
		Cluster: quotaClusterID, Namespace: "web-frontend", Quota: map[string]string{"limitsCpu": "4"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"no_changes","message":"The namespace already has the requested quota."}`, result.Content[0].(*mcp.TextContent).Text)
}

func TestUpdateNamespaceQuotaErrors(t *testing.T) {
	siblingAnnotation := `{"limit":{"limitsCpu":"8","extended":{"requests.nvidia.com/gpu":"0"}}}`

	tests := map[string]struct {
		project       *unstructured.Unstructured
		namespaces    []*unstructured.Unstructured
		params        updateNamespaceQuotaParams
		expectedError string
	}{
		"nothing to update": {
			project:       namespaceQuotaTestProject(),
			namespaces:    []*unstructured.Unstructured{namespaceWithQuota("web-frontend", "")},
			params:        updateNamespaceQuotaParams{Namespace: "web-frontend"},
			expectedError: "nothing to update",
		},
		"exceeds the project quota with the other namespaces": {
			project:       namespaceQuotaTestProject(),
			namespaces:    []*unstructured.Unstructured{namespaceWithQuota("web-frontend", ""), namespaceWithQuota("web-api", siblingAnnotation)},
			params:        updateNamespaceQuotaParams{Namespace: "web-frontend", Quota: map[string]string{"limitsCpu": "3"}},
			expectedError: `would add up to 11 of "limitsCpu", exceeding the project quota of 10`,
		},
		"resource without a namespace default": {
			project:       namespaceQuotaTestProject(),
			namespaces:    []*unstructured.Unstructured{namespaceWithQuota("web-frontend", "")},
			params:        updateNamespaceQuotaParams{Namespace: "web-frontend", Quota: map[string]string{"limitsMemory": "1Gi"}},
			expectedError: `"limitsMemory" has no namespace default`,
		},
		"project without a quota": {
			project:       quotaTestProject(map[string]any{}),
			namespaces:    []*unstructured.Unstructured{namespaceWithQuota("web-frontend", "")},
			params:        updateNamespaceQuotaParams{Namespace: "web-frontend", Quota: map[string]string{"limitsCpu": "3"}},
			expectedError: "has no resource quota",
		},
		"negative quota": {
			project:       namespaceQuotaTestProject(),
			namespaces:    []*unstructured.Unstructured{namespaceWithQuota("web-frontend", "")},
			params:        updateNamespaceQuotaParams{Namespace: "web-frontend", Quota: map[string]string{"limitsCpu": "-1"}},
			expectedError: "can't be negative",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			objs := []*unstructured.Unstructured{test.project}
			objs = append(objs, test.namespaces...)
			tools, _ := quotaTestToolsFromUnstructured(objs...)
			test.params.Cluster = quotaClusterID

			_, _, err := tools.updateNamespaceQuota(middleware.WithToken(t.Context(), quotaToken), &mcp.CallToolRequest{}, test.params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.expectedError)
		})
	}
}

func TestUpdateNamespaceQuotaFailsWhenListingNamespacesFails(t *testing.T) {
	tools, dynClient := quotaTestTools(namespaceQuotaTestProject(), namespaceWithQuota("web-frontend", ""))
	dynClient.PrependReactor("list", "namespaces", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	})
	ctx := middleware.WithToken(t.Context(), quotaToken)
	params := updateNamespaceQuotaParams{Cluster: quotaClusterID, Namespace: "web-frontend", Quota: map[string]string{"limitsCpu": "6"}}

	_, _, err := tools.updateNamespaceQuota(ctx, &mcp.CallToolRequest{}, params)
	require.ErrorContains(t, err, "failed to list the namespaces")

	_, _, err = tools.updateNamespaceQuotaPlan(ctx, &mcp.CallToolRequest{}, params)
	require.ErrorContains(t, err, "failed to list the namespaces")

	updated, err := tools.client.GetResource(ctx, client.GetParams{Cluster: quotaClusterID, Kind: "namespace", Name: "web-frontend", Token: quotaToken})
	require.NoError(t, err)
	assert.Empty(t, updated.GetAnnotations()[namespaceQuotaAnnotation], "the namespace must not be patched")
}
