package rbac

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapGetCurrentUser = zap.String("tool", "getCurrentUser")

const (
	// Rancher adds these keys to the extra info of authenticated users.
	extraLoginName   = "username"
	extraPrincipalID = "principalid"
)

// currentUser is the identity of the caller as reported by Rancher.
type currentUser struct {
	// UserID is the Rancher user ID, the metadata.name of the User resource.
	UserID string `json:"userId"`
	// LoginName is the name the user logged in with, as reported by the provider that issued the token.
	// It can differ from User.username, e.g. an email address for a user who logged in through OIDC.
	LoginName string `json:"loginName,omitempty"`
	// PrincipalIDs are the principals Rancher reports for the token, e.g. local://u-abc or openldap_user://uid=jo,dc=example.
	// They are not necessarily the provider the token was issued through: a token issued by a local login has been
	// seen to report the user's Keycloak principal.
	PrincipalIDs []string `json:"principalIds,omitempty"`
	// PrincipalProviders are the providers of PrincipalIDs, e.g. local or openldap. They are named differently from
	// getUser's authProviders, which are every provider the user can log in with.
	PrincipalProviders []string `json:"principalProviders,omitempty"`
	// Groups are the group principals the caller belongs to, excluding Kubernetes and Rancher system groups.
	Groups []string `json:"groups,omitempty"`
}

// getCurrentUser returns the identity of the token used to call Rancher.
func (t *Tools) getCurrentUser(ctx context.Context, toolReq *mcp.CallToolRequest, params struct{}) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getCurrentUser called")

	userInfo, err := t.reviewSelf(ctx)
	if err != nil {
		return nil, nil, err
	}

	user, err := currentUserFromUserInfo(userInfo)
	if err != nil {
		zap.L().Error("unexpected self subject review response", zapGetCurrentUser, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponseAny(user, response.UIContext{
		Kind:    "User",
		Cluster: "local",
		Name:    user.UserID,
		Type:    userSteveType,
	})
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetCurrentUser, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// reviewSelf returns the user info of the token used to call Rancher.
//
// It sends a SelfSubjectReview to the local cluster through Rancher, which authenticates the token
// and returns the user it belongs to. This works for any valid token, including users without access
// to the local cluster.
func (t *Tools) reviewSelf(ctx context.Context) (authenticationv1.UserInfo, error) {
	token := middleware.Token(ctx)
	if token == "" {
		return authenticationv1.UserInfo{}, errors.New("no Rancher credentials were supplied with this request, so the current user cannot be determined")
	}

	clientset, err := t.client.CreateClientSet(ctx, token, "local")
	if err != nil {
		zap.L().Error("failed to create clientset", zapGetCurrentUser, zap.Error(err))
		return authenticationv1.UserInfo{}, err
	}

	review, err := clientset.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return authenticationv1.UserInfo{}, currentUserError(err)
	}

	return review.Status.UserInfo, nil
}

// currentUserError logs err at a level matching its cause and returns an error with a message that
// says what to do about it. The returned error wraps err, so callers can still classify it.
// Rejected credentials are explained by the server's ToolErrors middleware, as they are for every tool.
func currentUserError(err error) error {
	switch {
	case apierrors.IsUnauthorized(err):
		zap.L().Warn("token rejected looking up current user", zapGetCurrentUser, zap.Error(err))
		return fmt.Errorf("looking up the current user: %w", err)
	case apierrors.IsForbidden(err):
		zap.L().Info("forbidden looking up current user", zapGetCurrentUser, zap.Error(err))
		return fmt.Errorf("rancher accepted the credentials but did not allow the current user to be looked up: %w", err)
	default:
		zap.L().Error("failed to look up current user", zapGetCurrentUser, zap.Error(err))
		return fmt.Errorf("looking up the current user: %w", err)
	}
}

// currentUserFromUserInfo converts the user info Rancher returns in a SelfSubjectReview.
//
// Rancher sets the username to the user ID and adds the login name and principal ID as extra info.
func currentUserFromUserInfo(info authenticationv1.UserInfo) (currentUser, error) {
	if info.Username == "" {
		return currentUser{}, errors.New("rancher did not return a user for the credentials")
	}

	user := currentUser{
		UserID:       info.Username,
		PrincipalIDs: info.Extra[extraPrincipalID],
	}
	if loginNames := info.Extra[extraLoginName]; len(loginNames) > 0 {
		user.LoginName = loginNames[0]
	}

	for _, principalID := range user.PrincipalIDs {
		if provider := providerFromPrincipalID(principalID); provider != "" && !slices.Contains(user.PrincipalProviders, provider) {
			user.PrincipalProviders = append(user.PrincipalProviders, provider)
		}
	}

	for _, group := range info.Groups {
		// system:authenticated, system:cattle:authenticated etc. apply to every user so say nothing about this one.
		if !strings.HasPrefix(group, "system:") {
			user.Groups = append(user.Groups, group)
		}
	}

	return user, nil
}

// providerFromPrincipalID returns the authentication provider of a Rancher principal ID, which has the
// form <provider>_user://<id> for external providers, or local://<id> and system://<id> otherwise.
// It returns an empty string if the principal ID has no provider.
func providerFromPrincipalID(principalID string) string {
	scheme, _, found := strings.Cut(principalID, "://")
	if !found {
		return ""
	}

	return strings.TrimSuffix(scheme, "_user")
}
