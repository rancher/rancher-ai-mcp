package rbac

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
)

const ldapGroupID = "openldap_group://cn=liz-provisioning,ou=groups,dc=example,dc=org"

// principalSearchServer answers principal searches with response, and records the last request body.
func principalSearchServer(t *testing.T, status int, response string, gotBody *map[string]string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v3/principals", r.URL.Path)
		assert.Equal(t, "search", r.URL.Query().Get("action"))

		if gotBody != nil {
			assert.NoError(t, json.NewDecoder(r.Body).Decode(gotBody))
		}

		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func TestResolveGroupPrincipal(t *testing.T) {
	tests := map[string]struct {
		group         string
		status        int
		response      string
		expected      string
		expectedError string
		expectSearch  bool
	}{
		"a principal ID is used without a search": {
			group:    ldapGroupID,
			expected: ldapGroupID,
		},
		"a local principal ID is used without a search": {
			group:    "local://g-abc",
			expected: "local://g-abc",
		},
		"a name is resolved through principal search": {
			group:        "liz-provisioning",
			status:       http.StatusOK,
			response:     `{"data": [{"id": "` + ldapGroupID + `", "name": "liz-provisioning", "principalType": "group"}]}`,
			expected:     ldapGroupID,
			expectSearch: true,
		},
		"only exact name matches of groups count": {
			group:  "liz",
			status: http.StatusOK,
			response: `{"data": [
				{"id": "openldap_group://cn=liz-provisioning,ou=groups,dc=example,dc=org", "name": "liz-provisioning", "principalType": "group"},
				{"id": "openldap_user://uid=liz,dc=example,dc=org", "name": "liz", "principalType": "user"},
				{"id": "openldap_group://cn=Liz,ou=groups,dc=example,dc=org", "name": "Liz", "principalType": "group"}
			]}`,
			expected:     "openldap_group://cn=Liz,ou=groups,dc=example,dc=org",
			expectSearch: true,
		},
		"a group that does not exist": {
			group:         "nobody",
			status:        http.StatusOK,
			response:      `{"data": [{"id": "openldap_group://cn=nobody-else,dc=example,dc=org", "name": "nobody-else", "principalType": "group"}]}`,
			expectedError: `group "nobody" not found`,
			expectSearch:  true,
		},
		"an ambiguous name": {
			group:  "devs",
			status: http.StatusOK,
			response: `{"data": [
				{"id": "github_team://1", "name": "devs", "principalType": "group"},
				{"id": "openldap_group://cn=devs,dc=example,dc=org", "name": "devs", "principalType": "group"}
			]}`,
			expectedError: `2 groups match "devs", specify one of the group principal IDs: github_team://1, openldap_group://cn=devs,dc=example,dc=org`,
			expectSearch:  true,
		},
		"a failed search": {
			group:         "devs",
			status:        http.StatusForbidden,
			response:      `{"message": "forbidden"}`,
			expectedError: `failed to search for group "devs"`,
			expectSearch:  true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var gotBody map[string]string
			url := fakeURL
			if tt.expectSearch {
				url = principalSearchServer(t, tt.status, tt.response, &gotBody)
			}

			tools, _ := newFakeTools(t, withRancherURL(url))
			got, err := tools.resolveGroupPrincipal(middleware.WithToken(t.Context(), fakeToken), tt.group)

			if tt.expectSearch {
				assert.Equal(t, map[string]string{"name": tt.group, "principalType": "group"}, gotBody)
			}
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestCreateGlobalRoleBindingResolvesGroupName(t *testing.T) {
	url := principalSearchServer(t, http.StatusOK,
		`{"data": [{"id": "`+ldapGroupID+`", "name": "liz-provisioning", "principalType": "group"}]}`, nil)
	tools, _ := newFakeTools(t, withRancherURL(url), withObjects(testUsers()...))

	result, _, err := tools.createGlobalRoleBindingPlan(
		middleware.WithToken(t.Context(), fakeToken),
		test.NewCallToolRequest(fakeURL),
		createGRBParams{GlobalRole: "user", Group: "liz-provisioning"},
	)
	require.NoError(t, err)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"groupPrincipalName":"`+ldapGroupID+`"`)
}

func TestCreateProjectRoleTemplateBindingResolvesGroupName(t *testing.T) {
	url := principalSearchServer(t, http.StatusOK,
		`{"data": [{"id": "`+ldapGroupID+`", "name": "liz-provisioning", "principalType": "group"}]}`, nil)
	tools, _ := newFakeTools(t, withRancherURL(url), withObjects(prtbTestObjects()...))

	result, _, err := tools.createProjectRoleTemplateBindingPlan(
		middleware.WithToken(t.Context(), fakeToken),
		test.NewCallToolRequest(fakeURL),
		createPRTBParams{Cluster: "local", Project: "web", Group: "liz-provisioning", RoleTemplate: "project-owner"},
	)
	require.NoError(t, err)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"groupPrincipalName":"`+ldapGroupID+`"`)
}
