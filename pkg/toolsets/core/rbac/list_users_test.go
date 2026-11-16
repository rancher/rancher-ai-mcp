package rbac

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"

	k8stesting "k8s.io/client-go/testing"

	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

func callListUsers(t *testing.T, tools *Tools, params listUsersParams) (listUsersResult, []response.UIContext, error) {
	t.Helper()

	resp, err := callTool[listUsersResult](t, tools.listUsers, params)

	return resp.LLM, resp.UIContext, err
}

func TestListUsers(t *testing.T) {
	t.Run("system accounts are excluded and counted", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, uiContext, err := callListUsers(t, tools, listUsersParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"u-admin", "u-badlabel", "u-ldap", "u-shared1", "u-shared2"}, ids(result.Users))
		assert.Equal(t, 5, result.Total)
		assert.Equal(t, 1, result.SystemAccountsExcluded)
		assert.Equal(t, visibilityFull, result.Visibility)
		assert.Nil(t, result.NextOffset)
		assert.Empty(t, result.Notes)
		assert.Len(t, uiContext, 5)
	})

	t.Run("system accounts can be included", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callListUsers(t, tools, listUsersParams{IncludeSystemAccounts: true})
		require.NoError(t, err)
		assert.Contains(t, ids(result.Users), "u-system")
		assert.Equal(t, 6, result.Total)
		assert.Equal(t, 0, result.SystemAccountsExcluded)
	})

	t.Run("each user has the getUser summary", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callListUsers(t, tools, listUsersParams{Limit: 1})
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		admin := result.Users[0]
		assert.Equal(t, "admin", admin.LoginName)
		assert.Equal(t, []string{"local"}, admin.AuthProviders)
		assert.Equal(t, lastLoginRecorded, admin.LastLogin.State)
		assert.Equal(t, []string{"admin", "user"}, admin.DirectGlobalRoles)
		assert.Empty(t, admin.MatchedOn)
	})

	t.Run("pages are counted over the full list", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callListUsers(t, tools, listUsersParams{Limit: 2, Offset: 2})
		require.NoError(t, err)
		assert.Equal(t, []string{"u-ldap", "u-shared1"}, ids(result.Users))
		assert.Equal(t, 5, result.Total)
		require.NotNil(t, result.NextOffset)
		assert.Equal(t, int64(4), *result.NextOffset)
		assert.Contains(t, result.Notes, "returned 2 of 5 users, to get the next page set offset=4")
	})

	t.Run("last page has no next offset", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callListUsers(t, tools, listUsersParams{Limit: 2, Offset: 4})
		require.NoError(t, err)
		assert.Equal(t, []string{"u-shared2"}, ids(result.Users))
		assert.Nil(t, result.NextOffset)
	})

	t.Run("more users than the default limit are all counted", func(t *testing.T) {
		var objs []runtime.Object
		for i := range 150 {
			objs = append(objs, user(fmt.Sprintf("u-%03d", i), fmt.Sprintf("user%d", i), nil))
		}

		tools, _ := newFakeTools(t, withCaller("u-000"), withObjects(objs...))

		result, _, err := callListUsers(t, tools, listUsersParams{})
		require.NoError(t, err)
		assert.Len(t, result.Users, 100)
		assert.Equal(t, 150, result.Total)
		require.NotNil(t, result.NextOffset)
		assert.Equal(t, int64(100), *result.NextOffset)
	})

	t.Run("forbidden list returns the caller's own user as limited", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-ldap"), withObjects(getUserObjects()...))
		forbid(fakeDynClient, "list", "users")
		forbid(fakeDynClient, "list", "globalrolebindings")

		result, _, err := callListUsers(t, tools, listUsersParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{"u-ldap"}, ids(result.Users))
		assert.Equal(t, visibilityLimited, result.Visibility)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, []string{"directGlobalRoles", "groupGlobalRoles"}, result.UnavailableFields)
		assert.Contains(t, result.Notes, "the credentials are not allowed to list users, so only the caller's own user is included and other users may exist")
	})

	t.Run("no users", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"))

		result, _, err := callListUsers(t, tools, listUsersParams{})
		require.NoError(t, err)
		assert.Equal(t, []userSummary{}, result.Users)
		assert.Equal(t, 0, result.Total)
	})

	t.Run("other errors are returned", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))
		fakeDynClient.PrependReactor("list", "users", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("connection refused")
		})

		_, _, err := callListUsers(t, tools, listUsersParams{})
		assert.ErrorContains(t, err, "connection refused")
	})
}

func TestListUsersGroupRoles(t *testing.T) {
	tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

	result, _, err := callListUsers(t, tools, listUsersParams{})
	require.NoError(t, err)

	idx := slices.IndexFunc(result.Users, func(u userSummary) bool { return u.UserID == "u-ldap" })
	require.GreaterOrEqual(t, idx, 0)

	ldap := result.Users[idx]
	assert.Equal(t, []groupGlobalRole{{GlobalRole: "admin", Group: "local://g-admins"}}, ldap.GroupGlobalRoles)
	assert.Equal(t, map[string]string{"openldap": "jo"}, ldap.ProviderUsernames)
	assert.Nil(t, ldap.Groups, "groups are left out of lists, since users can belong to many")
}
