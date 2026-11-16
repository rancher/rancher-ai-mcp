package rbac

import (
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"

	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
)

func TestGetCurrentUser(t *testing.T) {
	userGR := schema.GroupResource{Group: "authentication.k8s.io", Resource: "selfsubjectreviews"}

	tests := map[string]struct {
		token          string
		userInfo       authenticationv1.UserInfo
		reviewErr      error
		expectedResult string
		expectedError  string
		checkErr       func(error) bool
	}{
		"local user": {
			token: fakeToken,
			userInfo: authenticationv1.UserInfo{
				Username: "u-bwhgk",
				Groups:   []string{"system:authenticated", "system:cattle:authenticated"},
				Extra: map[string]authenticationv1.ExtraValue{
					"username":       {"testing"},
					"principalid":    {"local://u-bwhgk"},
					"requesttokenid": {"token-abc"},
				},
			},
			expectedResult: `{
				"llm": {
					"userId": "u-bwhgk",
					"loginName": "testing",
					"principalIds": ["local://u-bwhgk"],
					"principalProviders": ["local"]
				},
				"uiContext": [
					{"cluster": "local", "kind": "User", "name": "u-bwhgk", "namespace": "", "type": "management.cattle.io.user"}
				]
			}`,
		},
		"external user with groups": {
			token: fakeToken,
			userInfo: authenticationv1.UserInfo{
				Username: "u-ldap1",
				Groups:   []string{"system:authenticated", "openldap_group://cn=admins,dc=example,dc=org"},
				Extra: map[string]authenticationv1.ExtraValue{
					"username":    {"jo"},
					"principalid": {"openldap_user://uid=jo,dc=example,dc=org"},
				},
			},
			expectedResult: `{
				"llm": {
					"userId": "u-ldap1",
					"loginName": "jo",
					"principalIds": ["openldap_user://uid=jo,dc=example,dc=org"],
					"principalProviders": ["openldap"],
					"groups": ["openldap_group://cn=admins,dc=example,dc=org"]
				},
				"uiContext": [
					{"cluster": "local", "kind": "User", "name": "u-ldap1", "namespace": "", "type": "management.cattle.io.user"}
				]
			}`,
		},
		"user without extra info": {
			token:    fakeToken,
			userInfo: authenticationv1.UserInfo{Username: "u-abc"},
			expectedResult: `{
				"llm": {"userId": "u-abc"},
				"uiContext": [
					{"cluster": "local", "kind": "User", "name": "u-abc", "namespace": "", "type": "management.cattle.io.user"}
				]
			}`,
		},
		"no token": {
			token:         "",
			expectedError: "no Rancher credentials were supplied",
		},
		"invalid token": {
			token:         fakeToken,
			reviewErr:     apierrors.NewUnauthorized("Unauthorized"),
			expectedError: "looking up the current user: Unauthorized",
			checkErr:      apierrors.IsUnauthorized,
		},
		"forbidden": {
			token:         fakeToken,
			reviewErr:     apierrors.NewForbidden(userGR, "", errors.New("not allowed")),
			expectedError: "did not allow the current user to be looked up",
			checkErr:      apierrors.IsForbidden,
		},
		"other error": {
			token:         fakeToken,
			reviewErr:     errors.New("connection refused"),
			expectedError: "looking up the current user: connection refused",
		},
		"no user returned": {
			token:         fakeToken,
			userInfo:      authenticationv1.UserInfo{},
			expectedError: "rancher did not return a user",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newFakeTools(t, withUserInfo(tt.userInfo), withReviewError(tt.reviewErr))

			ctx := t.Context()
			if tt.token != "" {
				ctx = middleware.WithToken(ctx, tt.token)
			}

			result, _, err := tools.getCurrentUser(ctx, test.NewCallToolRequest(fakeURL), struct{}{})

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)

				if tt.checkErr != nil {
					assert.True(t, tt.checkErr(err), "error should still be classifiable: %v", err)
				}

				return
			}

			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

func TestProviderFromPrincipalID(t *testing.T) {
	tests := map[string]string{
		"local://u-abc":                       "local",
		"system://local":                      "system",
		"github_user://12345":                 "github",
		"activedirectory_user://ObjectGUID=x": "activedirectory",
		"keycloakoidc_user://jo":              "keycloakoidc",
		"no-scheme":                           "",
	}

	for principalID, expected := range tests {
		t.Run(principalID, func(t *testing.T) {
			assert.Equal(t, expected, providerFromPrincipalID(principalID))
		})
	}
}

// TestGetCurrentUserSendsToken checks that the caller's token is used for the review, since the review
// reports the identity of whichever token makes the request.
func TestGetCurrentUserSendsToken(t *testing.T) {
	tools, _ := newFakeTools(t, withUserInfo(authenticationv1.UserInfo{Username: "u-abc"}))

	_, _, err := tools.getCurrentUser(middleware.WithToken(t.Context(), "someone-elses-token"), test.NewCallToolRequest(fakeURL), struct{}{})

	require.ErrorContains(t, err, "invalid token")
}
