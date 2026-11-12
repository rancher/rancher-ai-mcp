package rbac

import (
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"

	k8stesting "k8s.io/client-go/testing"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

func getUserObjects() []runtime.Object {
	return []runtime.Object{
		user("u-admin", "admin", map[string]any{
			"displayName":  "Default Admin",
			"principalIds": []any{"local://u-admin"},
		}, lastLoginAt(testNow.Add(-3*24*time.Hour-time.Hour))),
		user("u-ldap", "", map[string]any{
			"displayName":  "Jo Smith",
			"principalIds": []any{"local://u-ldap", "openldap_user://uid=jo,dc=example,dc=org"},
			"enabled":      false,
		}),
		user("u-shared1", "", map[string]any{"displayName": "Shared Name", "principalIds": []any{"local://u-shared1"}}),
		user("u-shared2", "", map[string]any{"displayName": "shared name", "principalIds": []any{"local://u-shared2"}}),
		user("u-system", "", map[string]any{"principalIds": []any{"system://c-abc"}}, lastLoginAt(testNow)),
		user("u-badlabel", "bad", nil, withLabel(lastLoginLabel, "yesterday")),
		globalRoleBinding("grb-1", "admin", "userName", "u-admin"),
		globalRoleBinding("grb-2", "user", "userName", "u-admin"),
		globalRoleBinding("grb-3", "user", "userName", "u-ldap"),
		globalRoleBinding("grb-4", "admin", "groupPrincipalName", "local://g-admins"),
		globalRoleBinding("grb-5", "restricted-admin", "userPrincipalName", "openldap_user://uid=jo,dc=example,dc=org"),
		// u-ldap logs in to OpenLDAP as "jo", and is an admin through the local group g-admins.
		userAttribute("u-ldap", map[string]string{"openldap": "jo", "local": ""}, "openldap_group://cn=ops,dc=example,dc=org", "local://g-admins"),
	}
}

func callGetUser(t *testing.T, tools *Tools, query string) (getUserResult, []response.UIContext, error) {
	t.Helper()

	resp, err := callTool[getUserResult](t, tools.getUser, getUserParams{Username: query})

	return resp.LLM, resp.UIContext, err
}

func TestGetUser(t *testing.T) {
	t.Run("full response for a local user found by ID", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := tools.getUser(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), getUserParams{Username: "u-admin"})
		require.NoError(t, err)
		assert.JSONEq(t, `{
			"llm": {
				"users": [{
					"userId": "u-admin",
					"loginName": "admin",
					"displayName": "Default Admin",
					"authProviders": ["local"],
					"principalIds": ["local://u-admin"],
					"enabled": true,
					"createdAt": "2026-01-01T00:00:00Z",
					"lastLogin": {"state": "recorded", "time": "2026-11-03T11:00:00Z", "daysAgo": 3},
					"directGlobalRoles": ["admin", "user"],
					"matchedOn": "userId"
				}]
			},
			"uiContext": [
				{"cluster": "local", "kind": "User", "name": "u-admin", "namespace": "", "type": "management.cattle.io.user"}
			]
		}`, result.Content[0].(*mcp.TextContent).Text)
	})

	tests := map[string]struct {
		query             string
		expectedUsers     []string
		expectedMatchedOn string
		expectedNote      string
	}{
		"login name":                     {query: "admin", expectedUsers: []string{"u-admin"}, expectedMatchedOn: matchedOnLoginName},
		"display name ignores case":      {query: "jo smith", expectedUsers: []string{"u-ldap"}, expectedMatchedOn: matchedOnDisplayName},
		"principal ID":                   {query: "openldap_user://uid=jo,dc=example,dc=org", expectedUsers: []string{"u-ldap"}, expectedMatchedOn: matchedOnPrincipalID},
		"surrounding spaces are ignored": {query: "  admin ", expectedUsers: []string{"u-admin"}, expectedMatchedOn: matchedOnLoginName},
		"every match is returned": {
			query:             "Shared Name",
			expectedUsers:     []string{"u-shared1", "u-shared2"},
			expectedMatchedOn: matchedOnDisplayName,
			expectedNote:      `2 users match "Shared Name", ask which one is meant`,
		},
		"no match": {
			query:         "nobody",
			expectedUsers: []string{},
			expectedNote:  `no user has the user ID, login name, display name, principal ID or provider username "nobody"`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

			result, uiContext, err := callGetUser(t, tools, tt.query)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedUsers, ids(result.Users))
			assert.Len(t, uiContext, len(tt.expectedUsers))

			for _, u := range result.Users {
				assert.Equal(t, tt.expectedMatchedOn, u.MatchedOn)
			}

			if tt.expectedNote != "" {
				require.Len(t, result.Notes, 1)
				assert.Contains(t, result.Notes[0], tt.expectedNote)
			} else {
				assert.Empty(t, result.Notes)
			}
		})
	}

	t.Run("external user logs in with the external provider", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callGetUser(t, tools, "u-ldap")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		u := result.Users[0]
		assert.Equal(t, []string{"openldap"}, u.AuthProviders, "a user without a login name has no local password")
		assert.Empty(t, u.LoginName)
		assert.False(t, u.Enabled)
		assert.Equal(t, lastLogin{State: lastLoginNoneRecorded}, u.LastLogin)
		assert.Equal(t, []string{"restricted-admin", "user"}, u.DirectGlobalRoles)
	})

	t.Run("user without global role bindings has an empty list", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callGetUser(t, tools, "u-shared1")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.Equal(t, []string{}, result.Users[0].DirectGlobalRoles)
	})

	t.Run("system account", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callGetUser(t, tools, "u-system")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.True(t, result.Users[0].SystemAccount)
		assert.Empty(t, result.Users[0].AuthProviders)
		assert.Equal(t, lastLogin{State: lastLoginNotApplicable}, result.Users[0].LastLogin)
	})

	t.Run("malformed last login label", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callGetUser(t, tools, "bad")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.Equal(t, lastLogin{State: lastLoginUnknown}, result.Users[0].LastLogin)
	})

	t.Run("empty query", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		_, _, err := callGetUser(t, tools, " ")
		assert.ErrorContains(t, err, "username is required")
	})

	t.Run("global roles are marked unavailable when bindings cannot be listed", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))
		forbid(fakeDynClient, "list", "globalrolebindings")

		result, _, err := callGetUser(t, tools, "admin")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.Nil(t, result.Users[0].DirectGlobalRoles)
		assert.Equal(t, []string{"directGlobalRoles", "groupGlobalRoles"}, result.UnavailableFields)
		assert.Contains(t, result.Notes, "the credentials are not allowed to list global role bindings, so global roles are not included")
	})

	t.Run("a forbidden user ID falls back to searching all users", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))
		forbid(fakeDynClient, "get", "users")

		result, _, err := callGetUser(t, tools, "admin")
		require.NoError(t, err)
		assert.Equal(t, []string{"u-admin"}, ids(result.Users))
	})

	t.Run("when users cannot be listed the caller's own user is searched", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-ldap"), withObjects(getUserObjects()...))
		forbid(fakeDynClient, "list", "users")
		forbid(fakeDynClient, "list", "globalrolebindings")

		result, _, err := callGetUser(t, tools, "Jo Smith")
		require.NoError(t, err)
		assert.Equal(t, []string{"u-ldap"}, ids(result.Users))
		assert.Equal(t, matchedOnDisplayName, result.Users[0].MatchedOn)
		assert.Contains(t, result.Notes, "the credentials are not allowed to list users, so only the caller's own user was searched")
	})

	t.Run("when users cannot be listed other users cannot be looked up", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-ldap"), withObjects(getUserObjects()...))
		forbid(fakeDynClient, "list", "users")

		_, _, err := callGetUser(t, tools, "Default Admin")
		assert.EqualError(t, err, `the credentials are not allowed to look up other users, only their own user "u-ldap"`)
	})

	t.Run("other errors listing users are returned", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))
		fakeDynClient.PrependReactor("list", "users", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("connection refused")
		})

		_, _, err := callGetUser(t, tools, "admin")
		assert.ErrorContains(t, err, "connection refused")
	})
}

func TestAuthProvidersForUser(t *testing.T) {
	tests := map[string]struct {
		loginName    string
		principalIDs []string
		expected     []string
	}{
		"local user":                     {loginName: "kevin", principalIDs: []string{"local://u-a"}, expected: []string{"local"}},
		"user created by external login": {principalIDs: []string{"keycloakoidc_user://x", "local://u-a"}, expected: []string{"keycloakoidc"}},
		"local user linked to external provider": {
			loginName:    "admin",
			principalIDs: []string{"local://u-a", "keycloakoidc_user://x"},
			expected:     []string{"keycloakoidc", "local"},
		},
		"system account":      {principalIDs: []string{"system://c-abc"}, expected: []string{}},
		"no principals":       {loginName: "x", expected: []string{}},
		"duplicate providers": {principalIDs: []string{"github_user://1", "github_user://2"}, expected: []string{"github"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expected, authProvidersForUser(tt.loginName, tt.principalIDs))
		})
	}
}

func TestResolveUser(t *testing.T) {
	// The impostor has the target user's ID as its username, and is listed first.
	target := user("u-zzz", "alice", map[string]any{"displayName": "Alice Smith", "principalIds": []any{"openldap_user://uid=alice"}})
	impostor := user("u-aaa", "u-zzz", nil)
	duplicate1 := user("u-dup1", "shared", nil)
	duplicate2 := user("u-dup2", "shared", nil)

	tests := map[string]struct {
		query         string
		expectedUser  string
		expectedError string
	}{
		"a user ID is preferred to another user's username": {query: "u-zzz", expectedUser: "u-zzz"},
		"a unique username": {query: "alice", expectedUser: "u-zzz"},
		"a display name":    {query: "alice smith", expectedUser: "u-zzz"},
		"a principal ID":    {query: "openldap_user://uid=alice", expectedUser: "u-zzz"},
		"a username shared by more than one user is rejected": {
			query:         "shared",
			expectedError: `2 users match "shared", specify one of the user IDs: u-dup1, u-dup2`,
		},
		"no match": {
			query:         "nobody",
			expectedError: `user "nobody" not found, specify a username, user ID, display name, principal ID or provider username`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newFakeTools(t, withObjects(impostor, target, duplicate1, duplicate2))

			u, err := tools.resolveUser(middleware.WithToken(t.Context(), fakeToken), tt.query)
			if tt.expectedError != "" {
				assert.EqualError(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedUser, u.GetName())
		})
	}
}

// TestCreateGlobalRoleBindingResolvesUserIDBeforeUsername checks that a global role is never assigned to
// a user whose username matches another user's ID.
func TestCreateGlobalRoleBindingResolvesUserIDBeforeUsername(t *testing.T) {
	tools, _ := newFakeTools(t, withObjects(user("u-aaa", "u-zzz", nil), user("u-zzz", "alice", nil)))

	grb, err := tools.resolveGlobalRoleBindingObj(middleware.WithToken(t.Context(), fakeToken), createGRBParams{GlobalRole: "admin", User: "u-zzz"})
	require.NoError(t, err)
	assert.Equal(t, "u-zzz", grb.Object["userName"])
}

func TestGetUserAttributes(t *testing.T) {
	t.Run("an external user is found by their provider username, ignoring case", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callGetUser(t, tools, "JO")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.Equal(t, "u-ldap", result.Users[0].UserID)
		assert.Equal(t, matchedOnProviderUsername, result.Users[0].MatchedOn)
		assert.Empty(t, result.Notes)
	})

	t.Run("groups, roles through groups and provider usernames are included", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callGetUser(t, tools, "u-ldap")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		u := result.Users[0]
		assert.Equal(t, map[string]string{"openldap": "jo"}, u.ProviderUsernames, "the local username is the login name, and empty ones are left out")
		assert.Equal(t, []string{"local://g-admins", "openldap_group://cn=ops,dc=example,dc=org"}, u.Groups)
		assert.Equal(t, []groupGlobalRole{{GlobalRole: "admin", Group: "local://g-admins"}}, u.GroupGlobalRoles)
		assert.Equal(t, []string{"restricted-admin", "user"}, u.DirectGlobalRoles, "bindings by user principal are direct roles")
	})

	t.Run("a user ID is looked up without listing users or user attributes", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		_, _, err := callGetUser(t, tools, "u-ldap")
		require.NoError(t, err)

		var requests []string
		for _, action := range fakeDynClient.Actions() {
			requests = append(requests, action.GetVerb()+" "+action.GetResource().Resource)
		}

		assert.ElementsMatch(t, []string{"get users", "get userattributes", "list globalrolebindings"}, requests)
	})

	t.Run("a user without a user attribute has no groups", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callGetUser(t, tools, "u-admin")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.Nil(t, result.Users[0].Groups)
		assert.Nil(t, result.Users[0].GroupGlobalRoles)
		assert.Empty(t, result.UnavailableFields)
	})

	t.Run("user attributes that cannot be read are reported as unavailable", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))
		forbid(fakeDynClient, "list", "userattributes")

		result, _, err := callGetUser(t, tools, "Jo Smith")
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.Nil(t, result.Users[0].Groups)
		assert.Nil(t, result.Users[0].GroupGlobalRoles)
		assert.Equal(t, []string{"groups", "groupGlobalRoles", "providerUsernames"}, result.UnavailableFields)
		assert.Contains(t, result.Notes[0], "users cannot be found by provider username")
	})

	t.Run("bindings for a user's groups cannot be found without their user attribute", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))
		forbid(fakeDynClient, "get", "userattributes")

		_, err := tools.getUserSubjects(middleware.WithToken(t.Context(), fakeToken), "u-ldap")
		assert.EqualError(t, err, `failed to get groups for user "u-ldap": the credentials are not allowed to read user attributes`)
	})
}
