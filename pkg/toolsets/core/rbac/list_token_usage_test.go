package rbac

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
)

// tokenObjects returns users and tokens for testNow (2026-11-06T12:00:00Z).
func tokenObjects() []runtime.Object {
	ago := func(d time.Duration) string { return rfc3339(testNow.Add(-d)) }
	in := func(d time.Duration) string { return rfc3339(testNow.Add(d)) }
	day := 24 * time.Hour

	return append(getUserObjects(),
		v3Token("token-api", nil, map[string]any{
			"userId": "u-admin", "isDerived": true, "description": "ci", "authProvider": "local",
			"lastUsedAt": ago(time.Hour), "expiresAt": in(30 * day), "token": "secret-value",
		}),
		v3Token("token-session", map[string]string{tokenKindLabel: "session"}, map[string]any{
			"userId": "u-admin", "description": "UI session", "lastUsedAt": ago(2 * day), "expiresAt": in(day),
		}),
		v3Token("token-expired", nil, map[string]any{
			"userId": "u-ldap", "isDerived": true, "lastUsedAt": ago(20 * day), "expiresAt": ago(10 * day),
		}),
		v3Token("token-unused", nil, map[string]any{"userId": "u-ldap", "isDerived": true}),
		v3Token("token-disabled", nil, map[string]any{
			"userId": "u-shared1", "isDerived": true, "enabled": false, "lastUsedAt": ago(5 * day),
		}),
		extToken("token-kube",
			map[string]any{"userID": "u-admin", "kind": "kubeconfig", "description": "Kubeconfig token"},
			map[string]any{"lastUsedAt": ago(3 * day), "expired": false, "value": "ext-secret"},
		),
	)
}

func callTokenUsage(t *testing.T, tools *Tools, params listTokenUsageParams) (listTokenUsageResult, string, error) {
	t.Helper()

	resp, err := callTool[listTokenUsageResult](t, tools.listTokenUsage, params)

	return resp.LLM, resp.Text, err
}

func TestListTokenUsage(t *testing.T) {
	t.Run("every user's tokens from both stores", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(tokenObjects()...))

		result, text, err := callTokenUsage(t, tools, listTokenUsageParams{})
		require.NoError(t, err)

		assert.NotContains(t, text, "secret-value", "token values must never be returned")
		assert.NotContains(t, text, "ext-secret", "token values must never be returned")
		assert.Equal(t, "2026-11-06T12:00:00Z", result.EvaluatedAt)
		assert.Equal(t, tokenUsageCounts{UsersWithTokens: 3, Tokens: 6, ActiveTokens: 4, ExpiredTokens: 1, DisabledTokens: 1, NeverUsedTokens: 1}, result.Counts)
		assert.Equal(t, []string{"u-admin", "u-shared1", "u-ldap"}, ids(result.Users), "most recently used first")
		assert.Equal(t, visibilityFull, result.Visibility)
		assert.Empty(t, result.Notes)

		admin := result.Users[0]
		assert.Equal(t, "admin", admin.LoginName)
		assert.Equal(t, "Default Admin", admin.DisplayName)
		assert.Equal(t, 3, admin.ActiveTokens)
		assert.Equal(t, "2026-11-06T11:00:00Z", admin.LastUsedAt)
		assert.Equal(t, []string{"token-api", "token-session", "token-kube"}, ids(admin.Tokens))

		api, session, kube := admin.Tokens[0], admin.Tokens[1], admin.Tokens[2]
		assert.Equal(t, tokenUsage{
			Name: "token-api", Store: tokenStoreV3, Type: tokenTypeAPIKey, Description: "ci", AuthProvider: "local",
			ExpiresAt: "2026-12-06T12:00:00Z", Enabled: true, LastUsedAt: "2026-11-06T11:00:00Z", DaysSinceUse: new(int),
		}, api.withoutPrivate())
		assert.Equal(t, tokenTypeSession, session.Type)
		assert.Equal(t, tokenStoreExt, kube.Store)
		assert.Equal(t, "kubeconfig", kube.Type)
		assert.Equal(t, "Kubeconfig token", kube.Description)
		require.NotNil(t, kube.DaysSinceUse)
		assert.Equal(t, 3, *kube.DaysSinceUse)

		disabled := result.Users[1]
		assert.Equal(t, 0, disabled.ActiveTokens)
		require.Len(t, disabled.Tokens, 1)
		assert.False(t, disabled.Tokens[0].Enabled)
	})

	t.Run("expired tokens are counted but not listed", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(tokenObjects()...))

		result, _, err := callTokenUsage(t, tools, listTokenUsageParams{User: "Jo Smith"})
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		ldap := result.Users[0]
		assert.Equal(t, []string{"token-unused"}, ids(ldap.Tokens))
		assert.Equal(t, 1, ldap.ExpiredTokens)
		assert.Equal(t, 1, ldap.ActiveTokens)
		assert.Equal(t, "2026-10-17T12:00:00Z", ldap.LastUsedAt, "using a token that has since expired is still activity")
		assert.Nil(t, ldap.Tokens[0].DaysSinceUse, "never used")
	})

	t.Run("expired tokens can be listed", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(tokenObjects()...))

		result, _, err := callTokenUsage(t, tools, listTokenUsageParams{User: "u-ldap", IncludeExpired: true})
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.Equal(t, []string{"token-expired", "token-unused"}, ids(result.Users[0].Tokens))
		assert.True(t, result.Users[0].Tokens[0].Expired)
	})

	t.Run("unknown user", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(tokenObjects()...))

		_, _, err := callTokenUsage(t, tools, listTokenUsageParams{User: "nobody"})
		assert.ErrorContains(t, err, `user "nobody" not found, specify a username, user ID, display name, principal ID or provider username`)
	})

	t.Run("ambiguous user", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(tokenObjects()...))

		_, _, err := callTokenUsage(t, tools, listTokenUsageParams{User: "shared name"})
		assert.ErrorContains(t, err, "2 users match")
	})

	t.Run("pages users", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(tokenObjects()...))

		result, _, err := callTokenUsage(t, tools, listTokenUsageParams{Limit: 1})
		require.NoError(t, err)
		assert.Equal(t, []string{"u-admin"}, ids(result.Users))
		assert.Equal(t, 3, result.Counts.UsersWithTokens)
		require.NotNil(t, result.NextOffset)
		assert.Equal(t, int64(1), *result.NextOffset)
	})

	t.Run("users who cannot list tokens see their own v3 tokens from the v3 API", func(t *testing.T) {
		var gotPath string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"data": [
				{"id": "token-mine", "userId": "u-ldap", "isDerived": true, "current": true, "created": "2026-11-01T00:00:00Z",
				 "lastUsedAt": "2026-11-06T10:00:00Z", "expiresAt": "2027-01-01T00:00:00Z", "authProvider": "local"},
				{"id": "token-login", "userId": "u-ldap", "isDerived": false, "labels": {"authn.management.cattle.io/kind": "session"},
				 "lastUsedAt": "2026-11-05T12:00:00Z"}
			]}`))
		}))
		t.Cleanup(srv.Close)

		tools, fakeDynClient := newFakeTools(t, withRancherURL(srv.URL), withCaller("u-ldap"), withObjects(tokenObjects()...))
		forbid(fakeDynClient, "list", "tokens")
		forbid(fakeDynClient, "list", "users")

		result, _, err := callTokenUsage(t, tools, listTokenUsageParams{})
		require.NoError(t, err)
		assert.Equal(t, "/v3/tokens", gotPath)
		assert.Equal(t, visibilityLimited, result.Visibility)
		assert.Contains(t, result.Notes, "the credentials are not allowed to list every user's tokens, so only the caller's own tokens are included")
		assert.Contains(t, result.Notes, "the newer ext.cattle.io tokens, such as kubeconfig tokens, could not be listed, so they are not included")
		require.Len(t, result.Users, 1)
		mine := result.Users[0]
		assert.Equal(t, "u-ldap", mine.UserID)
		assert.Equal(t, "Jo Smith", mine.DisplayName, "the caller's own user can be read")
		assert.Equal(t, []string{"token-mine", "token-login"}, ids(mine.Tokens))
		assert.True(t, mine.Tokens[0].Current)
		assert.Equal(t, "2026-11-01T00:00:00Z", mine.Tokens[0].CreatedAt)
		assert.Equal(t, tokenTypeSession, mine.Tokens[1].Type)
	})

	t.Run("tokens without a user are skipped", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(append(tokenObjects(),
			v3Token("token-orphan", nil, map[string]any{"isDerived": true, "lastUsedAt": rfc3339(testNow)}))...))

		result, _, err := callTokenUsage(t, tools, listTokenUsageParams{})
		require.NoError(t, err)
		assert.NotContains(t, ids(result.Users), "")
		assert.Equal(t, 6, result.Counts.Tokens)
	})

	t.Run("the v3 API's next pages are followed", func(t *testing.T) {
		var srvURL string

		pages := map[string]string{
			"":               `{"data": [{"id": "token-1", "userId": "u-ldap", "isDerived": true}], "pagination": {"next": "NEXT/v3/tokens?marker=token-1"}}`,
			"marker=token-1": `{"data": [{"id": "token-2", "userId": "u-ldap", "isDerived": true}], "pagination": {}}`,
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.ReplaceAll(pages[r.URL.RawQuery], "NEXT", srvURL)))
		}))
		t.Cleanup(srv.Close)

		srvURL = srv.URL
		tools, fakeDynClient := newFakeTools(t, withRancherURL(srv.URL), withCaller("u-ldap"), withObjects(tokenObjects()...))
		forbid(fakeDynClient, "list", "tokens")

		result, _, err := callTokenUsage(t, tools, listTokenUsageParams{})
		require.NoError(t, err)
		require.Len(t, result.Users, 1)
		assert.ElementsMatch(t, []string{"token-1", "token-2"}, ids(result.Users[0].Tokens))
	})

	t.Run("callers who can list v3 tokens but not everything only see their own ext tokens", func(t *testing.T) {
		for name, opt := range map[string]fakeToolsOption{
			"access denied":       withoutListAll(),
			"access review fails": withAccessReviewError(errors.New("connection refused")),
		} {
			t.Run(name, func(t *testing.T) {
				tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(tokenObjects()...), opt)

				result, _, err := callTokenUsage(t, tools, listTokenUsageParams{})
				require.NoError(t, err)
				assert.Equal(t, visibilityLimited, result.Visibility)
				assert.Equal(t, []string{ownExtTokensNote}, result.Notes)
				assert.Equal(t, 6, result.Counts.Tokens, "the tokens that were listed are still reported")
			})
		}
	})

	t.Run("callers who only see their own v3 tokens are not told twice", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data": []}`))
		}))
		t.Cleanup(srv.Close)

		tools, fakeDynClient := newFakeTools(t, withRancherURL(srv.URL), withCaller("u-ldap"), withObjects(tokenObjects()...), withoutListAll())
		forbid(fakeDynClient, "list", "tokens")

		result, _, err := callTokenUsage(t, tools, listTokenUsageParams{})
		require.NoError(t, err)
		assert.Equal(t, visibilityLimited, result.Visibility)
		assert.NotContains(t, result.Notes, ownExtTokensNote)
	})

	t.Run("no tokens", func(t *testing.T) {
		tools, _ := newFakeTools(t, withCaller("u-admin"), withObjects(getUserObjects()...))

		result, _, err := callTokenUsage(t, tools, listTokenUsageParams{})
		require.NoError(t, err)
		assert.Equal(t, []userTokenUsage{}, result.Users)
		assert.Equal(t, tokenUsageCounts{}, result.Counts)
	})
}

// withoutPrivate returns the token with only its JSON fields, for comparing with an expected token.
func (u tokenUsage) withoutPrivate() tokenUsage {
	u.userID = ""
	u.lastUsed = time.Time{}

	return u
}
