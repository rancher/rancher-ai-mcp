package rbac

import (
	"context"
	"maps"
	"slices"
	"strings"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
)

// userAttributes are the UserAttributes of users, by user ID, or unavailable if the credentials are not
// allowed to read them.
//
// Rancher records in a user's UserAttribute, which has the same name as the user, the groups the user
// belongs to and the username they log in with at each provider. It is only created once the user logs in,
// so a user without one has no groups or provider usernames.
type userAttributes struct {
	byUser      map[string]*unstructured.Unstructured
	unavailable bool
}

// loadUserAttributes lists every UserAttribute. A 403 is not an error: the attributes are reported as
// unavailable instead.
func (t *Tools) loadUserAttributes(ctx context.Context) (userAttributes, error) {
	list, err := t.client.GetResources(ctx, client.ListParams{Cluster: "local", Kind: "userattribute", Token: middleware.Token(ctx)})
	if apierrors.IsForbidden(err) {
		zap.L().Info("forbidden listing user attributes", zap.Error(err))

		return userAttributes{unavailable: true}, nil
	}

	if err != nil {
		return userAttributes{}, err
	}

	attrs := userAttributes{byUser: make(map[string]*unstructured.Unstructured, len(list))}
	for _, ua := range list {
		attrs.byUser[ua.GetName()] = ua
	}

	return attrs, nil
}

// loadUserAttribute gets the UserAttribute of one user. A user without one is not an error, and nor is
// a 403: the attributes are reported as unavailable instead.
func (t *Tools) loadUserAttribute(ctx context.Context, userID string) (userAttributes, error) {
	ua, err := t.client.GetResource(ctx, client.GetParams{Cluster: "local", Kind: "userattribute", Name: userID, Token: middleware.Token(ctx)})

	switch {
	case err == nil:
		return userAttributes{byUser: map[string]*unstructured.Unstructured{userID: ua}}, nil
	case apierrors.IsNotFound(err):
		return userAttributes{byUser: map[string]*unstructured.Unstructured{}}, nil
	case apierrors.IsForbidden(err):
		zap.L().Info("forbidden getting user attribute", zap.String("user", userID), zap.Error(err))

		return userAttributes{unavailable: true}, nil
	default:
		return userAttributes{}, err
	}
}

// groups returns the group principals the user belongs to, sorted, or nil if they have none.
func (a userAttributes) groups(userID string) []string {
	return groupsFromUserAttribute(a.byUser[userID])
}

// providerUsernames returns the username the user logs in with at each external provider, or nil if
// there are none.
func (a userAttributes) providerUsernames(userID string) map[string]string {
	return providerUsernamesFromUserAttribute(a.byUser[userID])
}

// groupsFromUserAttribute returns the group principals recorded in ua, sorted and without duplicates.
//
// The GroupPrincipals and Items fields of UserAttribute have no JSON tags in Rancher's types, so they are
// stored with capitalized keys. Each item is a Principal, with its principal ID in metadata.name.
func groupsFromUserAttribute(ua *unstructured.Unstructured) []string {
	if ua == nil {
		return nil
	}

	var groups []string

	groupPrincipals, _, _ := unstructured.NestedMap(ua.Object, "GroupPrincipals")
	for _, provider := range groupPrincipals {
		providerMap, ok := provider.(map[string]any)
		if !ok {
			continue
		}

		items, _, _ := unstructured.NestedSlice(providerMap, "Items")
		for _, item := range items {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}

			if name := stringField(itemMap, "metadata", "name"); name != "" {
				groups = append(groups, name)
			}
		}
	}

	slices.Sort(groups)

	return slices.Compact(groups)
}

// providerUsernamesFromUserAttribute returns the username recorded in ua for each external provider.
//
// Rancher stores what each provider reported at login in ExtraByProvider, again with a capitalized key,
// e.g. an email address for an OIDC provider. The local provider's username is the user's login name, so
// it is left out.
func providerUsernamesFromUserAttribute(ua *unstructured.Unstructured) map[string]string {
	if ua == nil {
		return nil
	}

	var usernames map[string]string

	extra, _, _ := unstructured.NestedMap(ua.Object, "ExtraByProvider")
	for _, provider := range slices.Sorted(maps.Keys(extra)) {
		if provider == "local" {
			continue
		}

		values, _, _ := unstructured.NestedStringSlice(extra, provider, "username")
		if len(values) == 0 || values[0] == "" {
			continue
		}

		if usernames == nil {
			usernames = map[string]string{}
		}

		usernames[provider] = values[0]
	}

	return usernames
}

// providerUsernameMatches reports whether any of usernames is query. Providers such as LDAP and OIDC treat
// usernames and email addresses case-insensitively, so they are compared ignoring case.
func providerUsernameMatches(usernames map[string]string, query string) bool {
	for _, username := range usernames {
		if strings.EqualFold(username, query) {
			return true
		}
	}

	return false
}
