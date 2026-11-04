package rbac

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateGlobalRole(t *testing.T) {
	tests := map[string]struct {
		params        createGlobalRoleParams
		expectedObj   string
		expectedError string
	}{
		"create global role with rules and generated name": {
			params: createGlobalRoleParams{
				DisplayName: "Test Role",
				Description: "Testing Role",
				Rules:       []policyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"*"}}},
			},
			expectedObj: `{
				"apiVersion": "management.cattle.io/v3",
				"kind": "GlobalRole",
				"metadata": {"generateName": "gr-"},
				"displayName": "Test Role",
				"description": "Testing Role",
				"rules": [{"apiGroups": [""], "resources": ["configmaps"], "verbs": ["*"]}]
			}`,
		},
		"create global role with name, resource names and inherited cluster roles": {
			params: createGlobalRoleParams{
				Name:                  "my-role",
				DisplayName:           "My Role",
				Rules:                 []policyRule{{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get"}, ResourceNames: []string{"web"}}},
				InheritedClusterRoles: []string{"cluster-member"},
			},
			expectedObj: `{
				"apiVersion": "management.cattle.io/v3",
				"kind": "GlobalRole",
				"metadata": {"name": "my-role"},
				"displayName": "My Role",
				"rules": [{"apiGroups": ["apps"], "resources": ["deployments"], "verbs": ["get"], "resourceNames": ["web"]}],
				"inheritedClusterRoles": ["cluster-member"]
			}`,
		},
		"api groups default to the core group": {
			params: createGlobalRoleParams{
				DisplayName: "Core Role",
				Rules:       []policyRule{{Resources: []string{"configmaps"}, Verbs: []string{"get"}}},
			},
			expectedObj: `{
				"apiVersion": "management.cattle.io/v3",
				"kind": "GlobalRole",
				"metadata": {"generateName": "gr-"},
				"displayName": "Core Role",
				"rules": [{"apiGroups": [""], "resources": ["configmaps"], "verbs": ["get"]}]
			}`,
		},
		"missing display name": {
			params:        createGlobalRoleParams{Rules: []policyRule{{Resources: []string{"configmaps"}, Verbs: []string{"get"}}}},
			expectedError: "displayName must be specified",
		},
		"no rules or inherited cluster roles": {
			params:        createGlobalRoleParams{DisplayName: "Empty Role"},
			expectedError: "at least one rule or inherited cluster role must be specified",
		},
		"rule without verbs": {
			params:        createGlobalRoleParams{DisplayName: "Bad Role", Rules: []policyRule{{Resources: []string{"configmaps"}}}},
			expectedError: "rule 0 must specify at least one resource and one verb",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newTestTools(t)

			result, _, err := tools.createGlobalRole(
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
				LLM []json.RawMessage `json:"llm"`
			}
			require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &resp))
			require.Len(t, resp.LLM, 1)
			assert.JSONEq(t, tt.expectedObj, string(resp.LLM[0]))
		})
	}
}

func TestCreateGlobalRolePlan(t *testing.T) {
	tools := NewTools(nil, false)
	params := createGlobalRoleParams{
		DisplayName: "Test Role",
		Description: "Testing Role",
		Rules:       []policyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"*"}}},
	}

	result, _, err := tools.createGlobalRolePlan(t.Context(), test.NewCallToolRequest(fakeURL), params)
	require.NoError(t, err)

	var planResources []response.PlanResource
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &planResources))
	require.Len(t, planResources, 1)
	assert.Equal(t, response.OperationCreate, planResources[0].Type)
	assert.Equal(t, "GlobalRole", planResources[0].Resource.Kind)
	assert.Equal(t, "local", planResources[0].Resource.Cluster)
	payload := planResources[0].Payload.(map[string]any)
	assert.Equal(t, "Test Role", payload["displayName"])
	assert.Equal(t, "Testing Role", payload["description"])

	_, _, err = tools.createGlobalRolePlan(t.Context(), test.NewCallToolRequest(fakeURL), createGlobalRoleParams{DisplayName: "Empty Role"})
	assert.ErrorContains(t, err, "at least one rule or inherited cluster role must be specified")
}
