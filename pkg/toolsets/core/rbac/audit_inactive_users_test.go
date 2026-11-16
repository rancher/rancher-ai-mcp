package rbac

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8stesting "k8s.io/client-go/testing"

	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

// auditObjects returns users covering each audit outcome, for testNow (2026-11-06T12:00:00Z) and a
// 90 day window, so the cutoff is 2026-08-08T12:00:00Z.
func auditObjects() []runtime.Object {
	return []runtime.Object{
		user("u-active", "active", nil, lastLoginAt(daysAgo(10))),
		// u-boundary logged in a day inside a 90 day window; see TestAuditInactiveUsersExactCutoff for the cutoff itself.
		user("u-boundary", "boundary", nil, lastLoginAt(daysAgo(89))),
		user("u-stale", "stale", nil, lastLoginAt(daysAgo(100))),
		user("u-disabled", "disabled", map[string]any{"enabled": false}, lastLoginAt(daysAgo(200))),
		// created 2026-01-01, never logged in
		user("u-never", "never", nil),
		user("u-new", "new", nil, createdAt(daysAgo(5))),
		user("u-bad", "bad", nil, withLabel(lastLoginLabel, "yesterday")),
		user("u-system", "", map[string]any{"principalIds": []any{"system://c-abc"}}),
		globalRoleBinding("grb-1", "admin", "userName", "u-stale"),
		globalRoleBinding("grb-2", "user", "userName", "u-never"),
	}
}

func callAudit(t *testing.T, tools *Tools, params auditInactiveUsersParams) (auditInactiveUsersResult, []response.UIContext, error) {
	t.Helper()

	resp, err := callTool[auditInactiveUsersResult](t, tools.auditInactiveUsers, params)

	return resp.LLM, resp.UIContext, err
}

func TestAuditInactiveUsers(t *testing.T) {
	t.Run("classifies every user", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(auditObjects()...))

		result, uiContext, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)

		assert.Equal(t, 90, result.InactiveDays)
		assert.Equal(t, "2026-08-08T12:00:00Z", result.Cutoff)
		assert.Equal(t, "2026-11-06T12:00:00Z", result.EvaluatedAt)
		assert.Equal(t, auditCounts{
			UsersChecked:           7,
			Active:                 2,
			Inactive:               3,
			NeverLoggedIn:          1,
			AlreadyDisabled:        1,
			RecentlyCreatedNoLogin: 1,
			Undetermined:           1,
			SystemAccountsExcluded: 1,
		}, result.Counts)
		assert.Equal(t, []string{"u-never", "u-disabled", "u-stale"}, ids(result.InactiveUsers), "longest inactive first")
		assert.Equal(t, []string{"u-bad"}, ids(result.UndeterminedUsers))
		assert.Equal(t, visibilityFull, result.Visibility)
		assert.Nil(t, result.NextOffset)
		assert.True(t, result.TokenActivityIncluded)
		assert.Equal(t, []string{deletedTokensNote}, result.Notes)
		assert.Len(t, uiContext, 4)
	})

	t.Run("inactive user details", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(auditObjects()...))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)
		require.Len(t, result.InactiveUsers, 3)

		never, disabled, stale := result.InactiveUsers[0], result.InactiveUsers[1], result.InactiveUsers[2]
		assert.Equal(t, reasonNeverLoggedIn, never.Reason)
		require.NotNil(t, never.DaysInactive)
		assert.Equal(t, 309, *never.DaysInactive, "days since the account was created")
		assert.Equal(t, []string{"user"}, never.DirectGlobalRoles)

		assert.Equal(t, reasonLastLoginBeforeCutoff, disabled.Reason)
		assert.False(t, disabled.Enabled)

		assert.Equal(t, reasonLastLoginBeforeCutoff, stale.Reason)
		require.NotNil(t, stale.DaysInactive)
		assert.Equal(t, 100, *stale.DaysInactive)
		assert.Equal(t, []string{"admin"}, stale.DirectGlobalRoles)

		assert.Equal(t, reasonUndetermined, result.UndeterminedUsers[0].Reason)
		assert.Nil(t, result.UndeterminedUsers[0].DaysInactive)
		assert.Equal(t, []string{}, result.UndeterminedUsers[0].DirectGlobalRoles)
	})

	t.Run("a shorter window finds more users", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(auditObjects()...))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 3})
		require.NoError(t, err)
		assert.Equal(t, []string{"u-never", "u-disabled", "u-stale", "u-boundary", "u-active", "u-new"}, ids(result.InactiveUsers))
		assert.Equal(t, 0, result.Counts.RecentlyCreatedNoLogin, "u-new was created 5 days ago, before the cutoff")
		assert.Equal(t, 2, result.Counts.NeverLoggedIn)
	})

	t.Run("pages inactive users and counts all of them", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(auditObjects()...))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90, Limit: 2})
		require.NoError(t, err)
		assert.Equal(t, []string{"u-never", "u-disabled"}, ids(result.InactiveUsers))
		assert.Equal(t, 3, result.Counts.Inactive)
		require.NotNil(t, result.NextOffset)
		assert.Equal(t, int64(2), *result.NextOffset)
		assert.Contains(t, result.Notes, "returned 2 of 3 inactive users, to get the next page set offset=2")

		result, _, err = callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90, Limit: 2, Offset: 2})
		require.NoError(t, err)
		assert.Equal(t, []string{"u-stale"}, ids(result.InactiveUsers))
		assert.Nil(t, result.NextOffset)
	})

	t.Run("no inactive users", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(auditObjects()[0]))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)
		assert.Equal(t, []auditUser{}, result.InactiveUsers)
		assert.Equal(t, 1, result.Counts.UsersChecked)
		assert.Contains(t, result.Notes, deletedTokensNote, "the token caveat applies even when nobody is inactive")
	})

	for _, days := range []int{0, -1} {
		t.Run(fmt.Sprintf("inactiveDays %d is rejected", days), func(t *testing.T) {
			tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(auditObjects()...))

			_, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: days})
			assert.ErrorContains(t, err, "inactiveDays must be greater than 0")
		})
	}

	t.Run("undetermined users are capped at the limit", func(t *testing.T) {
		objs := []runtime.Object{}
		for _, name := range []string{"u-bad1", "u-bad2", "u-bad3"} {
			objs = append(objs, user(name, "", nil, withLabel(lastLoginLabel, "yesterday")))
		}

		tools, _ := newFakeTools(t, withCaller("u-bad1"), withObjects(objs...))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90, Limit: 2})
		require.NoError(t, err)
		assert.Equal(t, 3, result.Counts.Undetermined)
		assert.Equal(t, []string{"u-bad1", "u-bad2"}, ids(result.UndeterminedUsers))
		assert.Contains(t, result.Notes, "listed 2 of 3 undetermined users")
	})

	t.Run("forbidden list audits only the caller as limited", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-stale"), withObjects(auditObjects()...))
		forbid(fakeDynClient, "list", "users")
		forbid(fakeDynClient, "list", "globalrolebindings")

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)
		assert.Equal(t, visibilityLimited, result.Visibility)
		assert.Equal(t, 1, result.Counts.UsersChecked)
		assert.Equal(t, []string{"u-stale"}, ids(result.InactiveUsers))
		assert.Nil(t, result.InactiveUsers[0].DirectGlobalRoles)
		assert.Equal(t, []string{"directGlobalRoles", "groupGlobalRoles"}, result.UnavailableFields)
		assert.Contains(t, result.Notes, "the credentials are not allowed to list users, so only the caller's own user was audited and other users may be inactive")
	})
}

func TestAuditInactiveUsersTokenActivity(t *testing.T) {
	ago := func(days int) string { return rfc3339(testNow.AddDate(0, 0, -days)) }
	withTokens := func() []runtime.Object {
		return append(auditObjects(),
			// u-stale last logged in 100 days ago, but used an API key 5 days ago.
			v3Token("token-stale", nil, map[string]any{"userId": "u-stale", "isDerived": true, "lastUsedAt": ago(5)}),
			// u-never has never logged in, and last used a kubeconfig 120 days ago.
			extToken("token-never", map[string]any{"userID": "u-never", "kind": "kubeconfig"}, map[string]any{"lastUsedAt": ago(120)}),
			// u-bad's last login cannot be read, but it used a token yesterday.
			v3Token("token-bad", nil, map[string]any{"userId": "u-bad", "isDerived": true, "lastUsedAt": ago(1)}),
			// u-disabled used a token 150 days ago, after its last login 200 days ago.
			v3Token("token-disabled", nil, map[string]any{"userId": "u-disabled", "isDerived": true, "lastUsedAt": ago(150), "expiresAt": ago(140)}),
		)
	}

	t.Run("token use since the cutoff counts as activity", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(withTokens()...))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)
		assert.Equal(t, auditCounts{
			UsersChecked:           7,
			Active:                 4,
			ActiveByTokenUse:       2,
			Inactive:               2,
			NeverLoggedIn:          1,
			AlreadyDisabled:        1,
			RecentlyCreatedNoLogin: 1,
			SystemAccountsExcluded: 1,
		}, result.Counts)
		assert.Equal(t, []string{"u-disabled", "u-never"}, ids(result.InactiveUsers), "longest inactive first, counting token use")
		assert.Empty(t, result.UndeterminedUsers, "u-bad is active through its token")
	})

	t.Run("users active only through token use are listed", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(withTokens()...))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)
		require.Len(t, result.ActiveByTokenUseUsers, 2)

		bad, stale := result.ActiveByTokenUseUsers[0], result.ActiveByTokenUseUsers[1]
		assert.Equal(t, "u-bad", bad.UserID)
		assert.Equal(t, lastLoginUnknown, bad.LastLogin.State)
		assert.Equal(t, ago(1), bad.LastTokenUsedAt)

		assert.Equal(t, "u-stale", stale.UserID)
		assert.Equal(t, lastLoginRecorded, stale.LastLogin.State)
		require.NotNil(t, stale.LastLogin.DaysAgo)
		assert.Equal(t, 100, *stale.LastLogin.DaysAgo)
		assert.Equal(t, ago(5), stale.LastTokenUsedAt)
	})

	t.Run("the list of users active only through token use is limited", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(withTokens()...))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90, Limit: 1})
		require.NoError(t, err)
		assert.Len(t, result.ActiveByTokenUseUsers, 1)
		assert.Contains(t, result.Notes, "listed 1 of 2 users active only through token use")
	})

	t.Run("old token use is reported and counts towards days inactive", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(withTokens()...))

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)
		require.Len(t, result.InactiveUsers, 2)

		disabled, never := result.InactiveUsers[0], result.InactiveUsers[1]
		assert.Equal(t, reasonLastLoginBeforeCutoff, disabled.Reason)
		assert.Equal(t, ago(150), disabled.LastTokenUsedAt, "expired tokens still record activity")
		require.NotNil(t, disabled.DaysInactive)
		assert.Equal(t, 150, *disabled.DaysInactive)

		assert.Equal(t, reasonNeverLoggedIn, never.Reason)
		assert.Equal(t, ago(120), never.LastTokenUsedAt)
		require.NotNil(t, never.DaysInactive)
		assert.Equal(t, 120, *never.DaysInactive, "token use after the account was created")
	})

	t.Run("token activity can be skipped", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-active"), withObjects(withTokens()...))
		listedTokens := false

		fakeDynClient.PrependReactor("list", "tokens", func(k8stesting.Action) (bool, runtime.Object, error) {
			listedTokens = true
			return false, nil, nil
		})

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90, IncludeTokenActivity: new(false)})
		require.NoError(t, err)
		assert.False(t, listedTokens, "tokens should not be listed")
		assert.False(t, result.TokenActivityIncluded)
		assert.Equal(t, 3, result.Counts.Inactive)
		assert.Equal(t, 0, result.Counts.ActiveByTokenUse)
		assert.Empty(t, result.InactiveUsers[0].LastTokenUsedAt)
		assert.Equal(t, []string{apiTokenNote}, result.Notes)
	})

	t.Run("ext tokens filtered to the caller's own limit the audit", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-active"), withObjects(withTokens()...), withoutListAll())

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)
		assert.Equal(t, visibilityLimited, result.Visibility)
		assert.Contains(t, result.Notes, ownExtTokensNote)
	})

	t.Run("ext tokens that cannot be listed limit the audit", func(t *testing.T) {
		tools, fakeDynClient := newFakeTools(t, withCaller("u-active"), withObjects(withTokens()...))
		fakeDynClient.PrependReactor("list", "tokens", func(action k8stesting.Action) (bool, runtime.Object, error) {
			if action.GetResource().Group == "ext.cattle.io" {
				return true, nil, apierrors.NewForbidden(action.GetResource().GroupResource(), "", errors.New("not allowed"))
			}

			return false, nil, nil
		})

		result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
		require.NoError(t, err)
		assert.Equal(t, visibilityLimited, result.Visibility)
		assert.Contains(t, result.Notes, "the newer ext.cattle.io tokens, such as kubeconfig tokens, could not be listed, so they are not included")
		assert.Equal(t, 2, result.Counts.ActiveByTokenUse, "v3 tokens are still counted")
	})
}

// TestAuditInactiveUsersExactCutoff checks that activity exactly inactiveDays ago counts as inactive, since the
// user has then been inactive for at least inactiveDays days.
func TestAuditInactiveUsersExactCutoff(t *testing.T) {
	cutoff := daysAgo(90)
	tools, _ := newFakeTools(t, withCaller("u-login"), withObjects(
		user("u-login", "login", nil, lastLoginAt(cutoff)),
		user("u-token", "token", nil, lastLoginAt(daysAgo(200))),
		v3Token("token-at-cutoff", nil, map[string]any{"userId": "u-token", "isDerived": true, "lastUsedAt": rfc3339(cutoff)}),
		user("u-created", "created", nil, createdAt(cutoff)),
	))

	result, _, err := callAudit(t, tools, auditInactiveUsersParams{InactiveDays: 90})
	require.NoError(t, err)
	assert.Equal(t, []string{"u-created", "u-login", "u-token"}, ids(result.InactiveUsers))
	assert.Equal(t, 0, result.Counts.Active)
	assert.Equal(t, 0, result.Counts.RecentlyCreatedNoLogin)
}
