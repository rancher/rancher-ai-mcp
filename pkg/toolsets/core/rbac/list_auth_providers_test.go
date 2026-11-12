package rbac

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
)

// publicAuthProviders is an abridged /v3-public/authProviders response from Rancher v2.15 with Keycloak enabled.
const publicAuthProviders = `{
	"type": "collection",
	"resourceType": "authProvider",
	"data": [
		{
			"baseType": "authProvider",
			"id": "keycloakoidc",
			"type": "keyCloakOIDCProvider",
			"redirectUrl": "https://rancher.example.com/v1-oidc/keycloakoidc",
			"scopes": "openid profile email groups",
			"logoutAllSupported": true
		},
		{
			"baseType": "authProvider",
			"id": "local",
			"type": "localProvider",
			"logoutAllSupported": false
		}
	]
}`

func TestListAuthProviders(t *testing.T) {
	tests := map[string]struct {
		status         int
		body           string
		expectedResult string
		expectedError  string
	}{
		"enabled providers": {
			status: http.StatusOK,
			body:   publicAuthProviders,
			expectedResult: `{
				"llm": {
					"providers": [
						{"name": "keycloakoidc", "type": "keyCloakOIDCProvider"},
						{"name": "local", "type": "localProvider"}
					],
					"note": "Only the providers listed are enabled. The local provider can be disabled, so users can only log in with a Rancher username and password if local is listed."
				}
			}`,
		},
		"no providers": {
			status: http.StatusOK,
			body:   `{"type": "collection", "data": []}`,
			expectedResult: `{
				"llm": {
					"providers": [],
					"note": "Only the providers listed are enabled. The local provider can be disabled, so users can only log in with a Rancher username and password if local is listed."
				}
			}`,
		},
		"rancher error": {
			status:        http.StatusServiceUnavailable,
			body:          `{"message": "rancher is starting"}`,
			expectedError: "listing the enabled authentication providers",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var gotPath, gotAuth string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")

				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(srv.Close)

			tools, _ := newFakeTools(t, withRancherURL(srv.URL))

			result, _, err := tools.listAuthProviders(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), struct{}{})

			assert.Equal(t, "/v3-public/authProviders", gotPath)
			assert.NotContains(t, gotAuth, fakeToken, "the caller's token should not be sent to the public endpoint")

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

// TestListAuthProvidersWithoutToken checks that the providers can be listed when no credentials were supplied.
func TestListAuthProvidersWithoutToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(publicAuthProviders))
	}))
	t.Cleanup(srv.Close)

	tools, _ := newFakeTools(t, withRancherURL(srv.URL))

	_, _, err := tools.listAuthProviders(t.Context(), test.NewCallToolRequest(fakeURL), struct{}{})

	require.NoError(t, err)
}
