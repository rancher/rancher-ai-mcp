package rbac

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

func TestCreateGlobalRoleBinding(t *testing.T) {
	tests := map[string]struct {
		params        createGRBParams
		expectedObj   map[string]any
		expectedError string
	}{
		"assign global role to user": {
			params: createGRBParams{GlobalRole: "view-rancher-metrics", User: "u-user1"},
			expectedObj: map[string]any{
				"globalRoleName": "view-rancher-metrics",
				"userName":       "u-user1",
			},
		},
		"assign global role to user by username": {
			params: createGRBParams{GlobalRole: "view-rancher-metrics", User: "user2"},
			expectedObj: map[string]any{
				"globalRoleName": "view-rancher-metrics",
				"userName":       "u-user2",
			},
		},
		"unknown user": {
			params:        createGRBParams{GlobalRole: "user", User: "nobody"},
			expectedError: `user "nobody" not found, specify a username, user ID, display name, principal ID or provider username`,
		},
		"assign global role to group": {
			params: createGRBParams{GlobalRole: "user", Group: "github_team://1234"},
			expectedObj: map[string]any{
				"globalRoleName":     "user",
				"groupPrincipalName": "github_team://1234",
			},
		},
		"missing global role": {
			params:        createGRBParams{User: "u-user1"},
			expectedError: "globalRole must be specified",
		},
		"neither user nor group": {
			params:        createGRBParams{GlobalRole: "user"},
			expectedError: "exactly one of user or group must be specified",
		},
		"both user and group": {
			params:        createGRBParams{GlobalRole: "user", User: "u-user1", Group: "github_team://1234"},
			expectedError: "exactly one of user or group must be specified",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newFakeTools(t, withObjects(testUsers()...))

			result, _, err := tools.createGlobalRoleBinding(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)

			var resp struct {
				LLM []map[string]any `json:"llm"`
			}
			require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &resp))
			require.Len(t, resp.LLM, 1)
			assert.Equal(t, "GlobalRoleBinding", resp.LLM[0]["kind"])
			assert.Equal(t, "grb-", resp.LLM[0]["metadata"].(map[string]any)["generateName"])

			for k, v := range tt.expectedObj {
				assert.Equal(t, v, resp.LLM[0][k], k)
			}
		})
	}
}

func TestCreateGlobalRoleBindingPlan(t *testing.T) {
	tools, _ := newFakeTools(t, withObjects(testUsers()...))
	ctx := middleware.WithToken(t.Context(), fakeToken)

	result, _, err := tools.createGlobalRoleBindingPlan(ctx, test.NewCallToolRequest(fakeURL), createGRBParams{GlobalRole: "view-rancher-metrics", User: "user1"})
	require.NoError(t, err)

	var planResources []response.PlanResource
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &planResources))
	require.Len(t, planResources, 1)
	assert.Equal(t, response.OperationCreate, planResources[0].Type)
	assert.Equal(t, "GlobalRoleBinding", planResources[0].Resource.Kind)
	assert.Equal(t, "local", planResources[0].Resource.Cluster)
	payload, ok := planResources[0].Payload.(map[string]any)
	require.True(t, ok, "payload should be an object")
	assert.Equal(t, "view-rancher-metrics", payload["globalRoleName"])
	assert.Equal(t, "u-user1", payload["userName"])

	_, _, err = tools.createGlobalRoleBindingPlan(ctx, test.NewCallToolRequest(fakeURL), createGRBParams{GlobalRole: "user"})
	assert.ErrorContains(t, err, "exactly one of user or group must be specified")
}

func TestCreateGlobalRoleBindingAlreadyExists(t *testing.T) {
	deleting := globalRoleBinding("grb-deleting", "user", "groupPrincipalName", "github_team://1234")
	deleting.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})

	existing := append(testUsers(),
		globalRoleBinding("grb-user", "view-rancher-metrics", "userName", "u-user1"),
		globalRoleBinding("grb-group", "user", "groupPrincipalName", "github_team://5678"),
		globalRoleBinding("grb-principal", "edit", "userPrincipalName", "github_user://99"),
		globalRoleBinding("grb-other-principal", "audit", "userPrincipalName", "github_user://12345"),
		deleting,
	)

	tests := map[string]struct {
		params           createGRBParams
		expectedExisting string
	}{
		"user already has the role":                {params: createGRBParams{GlobalRole: "view-rancher-metrics", User: "user1"}, expectedExisting: "grb-user"},
		"group already has the role":               {params: createGRBParams{GlobalRole: "user", Group: "github_team://5678"}, expectedExisting: "grb-group"},
		"user has the role through a principal ID": {params: createGRBParams{GlobalRole: "edit", User: "user1"}, expectedExisting: "grb-principal"},
		"principal ID of another user":             {params: createGRBParams{GlobalRole: "audit", User: "user1"}},
		"different role":                           {params: createGRBParams{GlobalRole: "admin", User: "user1"}},
		"different user":                           {params: createGRBParams{GlobalRole: "view-rancher-metrics", User: "user2"}},
		"existing binding is deleting":             {params: createGRBParams{GlobalRole: "user", Group: "github_team://1234"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeClient := newFakeTools(t, withObjects(existing...))
			ctx := middleware.WithToken(t.Context(), fakeToken)
			req := test.NewCallToolRequest(fakeURL)
			grbCount := func() int {
				list, err := fakeClient.Resource(converter.K8sKindsToGVRs["globalrolebinding"]).List(ctx, metav1.ListOptions{})
				require.NoError(t, err)

				return len(list.Items)
			}
			before := grbCount()

			planResult, _, err := tools.createGlobalRoleBindingPlan(ctx, req, tt.params)
			require.NoError(t, err)

			planText := planResult.Content[0].(*mcp.TextContent).Text

			result, _, err := tools.createGlobalRoleBinding(ctx, req, tt.params)
			require.NoError(t, err)

			if tt.expectedExisting == "" {
				assert.Contains(t, planText, `"type":"create"`)
				assert.NotContains(t, planText, "no_changes")
				assert.Equal(t, before+1, grbCount())

				return
			}

			assert.Contains(t, planText, "no_changes")
			assert.Contains(t, planText, tt.expectedExisting)
			assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "no changes needed")
			assert.Equal(t, before, grbCount())
		})
	}
}
