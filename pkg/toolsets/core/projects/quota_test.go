package projects

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
)

const (
	quotaClusterID = "c-quota"
	quotaProjectID = "p-quota"
	quotaToken     = "fakeToken"
)

func quotaTestProject(spec map[string]any) *unstructured.Unstructured {
	spec["displayName"] = "web"

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Project",
		"metadata":   map[string]any{"name": quotaProjectID, "namespace": quotaClusterID},
		"spec":       spec,
	}}
}

func quotaTestTools(objs ...runtime.Object) (*Tools, *dynamicfake.FakeDynamicClient) {
	cluster := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": quotaClusterID},
	}}
	fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}: "ClusterList",
		{Group: "management.cattle.io", Version: "v3", Resource: "projects"}: "ProjectList",
		{Version: "v1", Resource: "namespaces"}:                              "NamespaceList",
		{Version: "v1", Resource: "resourcequotas"}:                          "ResourceQuotaList",
	}, append([]runtime.Object{cluster}, objs...)...)
	c := &client.Client{DynClientCreator: func(*rest.Config) (dynamic.Interface, error) {
		return fakeDynClient, nil
	}}

	return NewTools(newFakeToolsClient(c, quotaToken), false), fakeDynClient
}

func quotaLimit(cpu, gpu string) map[string]any {
	return map[string]any{"limit": map[string]any{
		"limitsCpu": cpu,
		"extended":  map[string]any{"requests.nvidia.com/gpu": gpu},
	}}
}

func TestUpdateProjectQuota(t *testing.T) {
	project := quotaTestProject(map[string]any{
		"resourceQuota":                 map[string]any{"limit": map[string]any{"limitsCpu": "10", "limitsMemory": "20Gi"}},
		"namespaceDefaultResourceQuota": map[string]any{"limit": map[string]any{"limitsCpu": "2", "limitsMemory": "4Gi"}},
	})
	tools, _ := quotaTestTools(project)
	ctx := middleware.WithToken(t.Context(), quotaToken)

	params := updateProjectQuotaParams{
		Cluster:               quotaClusterID,
		Project:               "web",
		ProjectQuota:          map[string]string{"limitsCpu": "20", "nvidia.com/gpu": "4"},
		NamespaceDefaultQuota: map[string]string{"nvidia.com/gpu": "1"},
	}

	_, _, err := tools.updateProjectQuota(ctx, &mcp.CallToolRequest{}, params)
	require.NoError(t, err)

	updated, err := tools.client.GetResource(ctx, client.GetParams{Cluster: LocalCluster, Kind: "project", Namespace: quotaClusterID, Name: quotaProjectID, Token: quotaToken})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"limitsCpu": "20", "limitsMemory": "20Gi", "requests.nvidia.com/gpu": "4"}, currentQuota(updated, "spec", "resourceQuota", "limit"))
	assert.Equal(t, map[string]string{"limitsCpu": "2", "limitsMemory": "4Gi", "requests.nvidia.com/gpu": "1"}, currentQuota(updated, "spec", "namespaceDefaultResourceQuota", "limit"))
}

func TestUpdateProjectQuotaPlan(t *testing.T) {
	project := quotaTestProject(map[string]any{
		"resourceQuota":                 quotaLimit("10", "2"),
		"namespaceDefaultResourceQuota": quotaLimit("2", "1"),
	})
	tools, _ := quotaTestTools(project)
	ctx := middleware.WithToken(t.Context(), quotaToken)

	result, _, err := tools.updateProjectQuotaPlan(ctx, &mcp.CallToolRequest{}, updateProjectQuotaParams{
		Cluster: quotaClusterID, Project: "web", ProjectQuota: map[string]string{"limitsCpu": "20"},
	})
	require.NoError(t, err)

	var plan []map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &plan))
	require.Len(t, plan, 1)
	assert.Equal(t, "update", plan[0]["type"])
	assert.Equal(t, "Project", plan[0]["resource"].(map[string]any)["kind"])

	// Requesting what is already in place reports no changes.
	result, _, err = tools.updateProjectQuotaPlan(ctx, &mcp.CallToolRequest{}, updateProjectQuotaParams{
		Cluster: quotaClusterID, Project: "web", ProjectQuota: map[string]string{"limitsCpu": "10"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"no_changes","message":"The project already has the requested quota."}`, result.Content[0].(*mcp.TextContent).Text)
}

func TestUpdateProjectQuotaErrors(t *testing.T) {
	tests := map[string]struct {
		spec          map[string]any
		params        updateProjectQuotaParams
		expectedError string
	}{
		"nothing to update": {
			spec:          map[string]any{},
			params:        updateProjectQuotaParams{},
			expectedError: "nothing to update",
		},
		"unknown resource": {
			spec:          map[string]any{},
			params:        updateProjectQuotaParams{ProjectQuota: map[string]string{"bogus": "1"}},
			expectedError: "unsupported quota resource",
		},
		"invalid quantity": {
			spec:          map[string]any{},
			params:        updateProjectQuotaParams{ProjectQuota: map[string]string{"limitsCpu": "lots"}},
			expectedError: "invalid quantity",
		},
		"project quota without namespace default": {
			spec:          map[string]any{},
			params:        updateProjectQuotaParams{ProjectQuota: map[string]string{"limitsCpu": "20"}},
			expectedError: "requires a namespace default quota",
		},
		"new resource without namespace default": {
			spec: map[string]any{
				"resourceQuota":                 quotaLimit("10", "2"),
				"namespaceDefaultResourceQuota": quotaLimit("2", "1"),
			},
			params:        updateProjectQuotaParams{ProjectQuota: map[string]string{"limitsMemory": "8Gi"}},
			expectedError: `"limitsMemory" has a project quota but no namespace default`,
		},
		"namespace default exceeds project quota": {
			spec: map[string]any{
				"resourceQuota":                 quotaLimit("10", "2"),
				"namespaceDefaultResourceQuota": quotaLimit("2", "1"),
			},
			params:        updateProjectQuotaParams{ProjectQuota: map[string]string{"limitsCpu": "1"}},
			expectedError: "exceeds the project quota",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := quotaTestTools(quotaTestProject(test.spec))
			ctx := middleware.WithToken(t.Context(), quotaToken)
			test.params.Cluster = quotaClusterID
			test.params.Project = "web"

			_, _, err := tools.updateProjectQuota(ctx, &mcp.CallToolRequest{}, test.params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.expectedError)
		})
	}
}

func quotaTestNamespace(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": name, "labels": map[string]any{projectIDKey: quotaProjectID}},
	}}
}

func quotaTestResourceQuota(namespace string, hard, used map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ResourceQuota",
		"metadata":   map[string]any{"name": "default", "namespace": namespace},
		"status":     map[string]any{"hard": hard, "used": used},
	}}
}

func TestAuditProjectQuota(t *testing.T) {
	project := quotaTestProject(map[string]any{
		"resourceQuota":                 map[string]any{"limit": map[string]any{"limitsCpu": "10", "pods": "50"}},
		"namespaceDefaultResourceQuota": map[string]any{"limit": map[string]any{"limitsCpu": "5", "pods": "25"}},
	})
	tools, _ := quotaTestTools(
		project,
		quotaTestNamespace("a"), quotaTestNamespace("b"), quotaTestNamespace("unlimited"),
		quotaTestResourceQuota("a", map[string]any{"limits.cpu": "5", "pods": "25"}, map[string]any{"limits.cpu": "4750m", "pods": "20"}),
		quotaTestResourceQuota("b", map[string]any{"limits.cpu": "4", "pods": "25"}, map[string]any{"limits.cpu": "0", "pods": "1"}),
	)
	ctx := middleware.WithToken(t.Context(), quotaToken)

	result, _, err := tools.auditProjectQuota(ctx, &mcp.CallToolRequest{}, auditProjectQuotaParams{Cluster: quotaClusterID, Project: "web"})
	require.NoError(t, err)

	var out struct {
		LLM struct {
			Resources []map[string]any `json:"resources"`
			Findings  []string         `json:"findings"`
			Without   []string         `json:"namespacesWithoutQuota"`
			Namespace []map[string]any `json:"namespaces"`
		} `json:"llm"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &out))
	audit := out.LLM

	require.Len(t, audit.Resources, 2)
	cpu := audit.Resources[0]
	assert.Equal(t, "limitsCpu", cpu["resource"])
	assert.Equal(t, "10", cpu["projectLimit"])
	assert.Equal(t, "9", cpu["allocated"])
	assert.Equal(t, "4750m", cpu["used"])
	assert.Equal(t, 90.0, cpu["allocatedPercent"])
	assert.Equal(t, 52.8, cpu["usedPercent"])

	pods := audit.Resources[1]
	assert.Equal(t, "pods", pods["resource"])
	assert.Equal(t, "50", pods["allocated"])
	assert.Equal(t, "21", pods["used"])

	assert.Equal(t, []string{"unlimited"}, audit.Without)
	assert.Len(t, audit.Namespace, 2)
	require.Len(t, audit.Findings, 3)
	assert.Contains(t, audit.Findings[0], "limitsCpu: 90% of the project limit is already allocated")
	assert.Contains(t, audit.Findings[1], "pods: 100% of the project limit is already allocated")
	assert.Contains(t, audit.Findings[2], "unlimited")
}
