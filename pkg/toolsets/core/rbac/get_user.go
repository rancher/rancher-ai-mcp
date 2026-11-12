package rbac

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapGetUser = zap.String("tool", "getUser")

// lastLoginLabel is the label Rancher sets on a User to the time of their last login, in seconds since the epoch.
const lastLoginLabel = "cattle.io/last-login"

// The fields a user can be matched on, reported in userSummary.MatchedOn.
const (
	matchedOnUserID      = "userId"
	matchedOnLoginName   = "loginName"
	matchedOnDisplayName = "displayName"
	matchedOnPrincipalID = "principalId"
	// matchedOnProviderUsername is a match on the username a user logs in with at an external provider.
	matchedOnProviderUsername = "providerUsername"
)

// The states of a user's last login, reported in lastLogin.State.
const (
	lastLoginRecorded      = "recorded"
	lastLoginNoneRecorded  = "noLoginRecorded"
	lastLoginUnknown       = "unknown"
	lastLoginNotApplicable = "notApplicable"
)

// ownUserOnlyNote explains a getUser result where only the caller's own user could be searched.
const ownUserOnlyNote = "the credentials are not allowed to list users, so only the caller's own user was searched"

type getUserParams struct {
	Username string `json:"username" jsonschema:"the user ID, login name, display name, principal ID, or username at an external provider (such as an email address for OIDC) of the user to retrieve"`
}

// getUserResult is the response of getUser.
type getUserResult struct {
	Users []userSummary `json:"users"`
	// UnavailableFields lists fields that are missing from every user because the credentials are not allowed to read them.
	UnavailableFields []string `json:"unavailableFields,omitempty"`
	Notes             []string `json:"notes,omitempty"`
}

// userSummary is the information about a user that answers common questions, without the rest of the User resource.
type userSummary struct {
	UserID string `json:"userId"`
	// LoginName is User.username. Users from external providers often have none.
	LoginName   string `json:"loginName,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	// ProviderUsernames are the usernames the user logs in with at each external provider, e.g. an email
	// address for an OIDC provider, as Rancher recorded them at the user's last login with that provider.
	ProviderUsernames map[string]string `json:"providerUsernames,omitempty"`
	// AuthProviders are the providers the user can log in with, see authProvidersForUser.
	AuthProviders []string  `json:"authProviders"`
	PrincipalIDs  []string  `json:"principalIds,omitempty"`
	SystemAccount bool      `json:"systemAccount,omitempty"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     string    `json:"createdAt,omitempty"`
	LastLogin     lastLogin `json:"lastLogin"`
	// DirectGlobalRoles are the global roles bound to the user, not including those bound to their groups.
	// It is nil, and omitted, if the credentials are not allowed to list global role bindings, and empty
	// if the user has none.
	DirectGlobalRoles []string `json:"directGlobalRoles,omitzero"`
	// GroupGlobalRoles are the global roles the user has through the groups they belong to. It is omitted if
	// there are none, or if they are unavailable, which unavailableFields reports.
	GroupGlobalRoles []groupGlobalRole `json:"groupGlobalRoles,omitempty"`
	// Groups are the group principals the user belongs to. Only getUser includes them, since users of
	// external providers can belong to many groups.
	Groups    []string `json:"groups,omitempty"`
	MatchedOn string   `json:"matchedOn,omitempty"`
}

// lastLogin is when a user last logged in, with a state so that a missing time is not mistaken for no login.
type lastLogin struct {
	State   string `json:"state"`
	Time    string `json:"time,omitempty"`
	DaysAgo *int   `json:"daysAgo,omitempty"`
}

// userMatches are the users that match a query, the UserAttributes loaded while matching them, and
// whether only the caller's own user could be searched.
type userMatches struct {
	users   []matchedUser
	attrs   userAttributes
	ownOnly bool
}

// matchedUser is a user that matches a getUser query, and the field it matched on.
type matchedUser struct {
	user      *unstructured.Unstructured
	matchedOn string
}

func (t *Tools) getUser(ctx context.Context, toolReq *mcp.CallToolRequest, params getUserParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getUser called")

	query := strings.TrimSpace(params.Username)
	if query == "" {
		return nil, nil, errors.New("username is required, specify a user ID, login name, display name, principal ID or provider username")
	}

	var (
		wg                 sync.WaitGroup
		found              userMatches
		roles              directGlobalRoles
		matchErr, rolesErr error
	)

	wg.Go(func() { found, matchErr = t.matchUsers(ctx, query) })
	wg.Go(func() { roles, rolesErr = t.loadDirectGlobalRoles(ctx) })
	wg.Wait()

	if err := errors.Join(matchErr, rolesErr); err != nil {
		zap.L().Error("failed to find users", zapGetUser, zap.Error(err))

		return nil, nil, err
	}

	matches, attrs := found.users, found.attrs

	result := getUserResult{Users: make([]userSummary, 0, len(matches))}
	if found.ownOnly {
		result.Notes = append(result.Notes, ownUserOnlyNote)
	}

	switch {
	case len(matches) == 0:
		result.Notes = append(result.Notes, fmt.Sprintf("no user has the user ID, login name, display name, principal ID or provider username %q", query))
	case len(matches) > 1:
		result.Notes = append(result.Notes, fmt.Sprintf("%d users match %q, ask which one is meant and use the user ID to tell them apart", len(matches), query))
	default:
	}

	now := t.now()
	userIDs := make([]string, 0, len(matches))

	for _, m := range matches {
		summary := describeUser(m.user, attrs, roles, now, summaryFull)
		summary.MatchedOn = m.matchedOn
		result.Users = append(result.Users, summary)
		userIDs = append(userIDs, summary.UserID)
	}

	if len(matches) > 0 {
		unavailable, notes := summaryLimits(attrs, roles, summaryFull)
		result.UnavailableFields = append(result.UnavailableFields, unavailable...)
		result.Notes = append(result.Notes, notes...)
	}

	mcpResponse, err := response.CreateMcpResponseAny(result, userUIContext(userIDs...)...)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetUser, zap.Error(err))

		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// matchUsers returns the users whose ID, login name, display name, principal ID or provider username
// matches query, sorted by user ID, with the UserAttributes of the matched users.
//
// User IDs are unique, so a user with the ID query is returned on its own without listing every user.
// Otherwise users and UserAttributes are listed in parallel. If the credentials are not allowed to list
// users, only the caller's own user is searched, and an error is returned if it does not match, since other
// users may.
func (t *Tools) matchUsers(ctx context.Context, query string) (userMatches, error) {
	token := middleware.Token(ctx)

	// Display names and principal IDs are not valid resource names, and requesting them would fail.
	if len(validation.IsDNS1123Subdomain(query)) == 0 {
		user, err := t.client.GetResource(ctx, client.GetParams{Cluster: "local", Kind: "user", Name: query, Token: token})

		switch {
		case err == nil:
			attrs, err := t.loadUserAttribute(ctx, user.GetName())
			if err != nil {
				return userMatches{}, err
			}

			return userMatches{users: []matchedUser{{user: user, matchedOn: matchedOnUserID}}, attrs: attrs}, nil
		case apierrors.IsNotFound(err), apierrors.IsForbidden(err):
			// Not a user ID, or the credentials cannot read that user. Rancher checks access before
			// existence, so a forbidden user may not exist either. Search all users instead.
		default:
			return userMatches{}, err
		}
	}

	var (
		wg                 sync.WaitGroup
		users              []*unstructured.Unstructured
		attrs              userAttributes
		usersErr, attrsErr error
	)

	wg.Go(func() {
		users, usersErr = t.client.GetResources(ctx, client.ListParams{Cluster: "local", Kind: "user", Token: token})
	})
	wg.Go(func() { attrs, attrsErr = t.loadUserAttributes(ctx) })
	wg.Wait()

	if apierrors.IsForbidden(usersErr) {
		zap.L().Info("forbidden listing users, searching own user", zapGetUser, zap.Error(usersErr))

		return t.matchOwnUser(ctx, query)
	}

	if err := errors.Join(usersErr, attrsErr); err != nil {
		return userMatches{}, err
	}

	var matches []matchedUser

	for _, user := range users {
		if matchedOn, ok := matchUser(user, attrs.providerUsernames(user.GetName()), query); ok {
			matches = append(matches, matchedUser{user: user, matchedOn: matchedOn})
		}
	}

	slices.SortFunc(matches, func(a, b matchedUser) int { return strings.Compare(a.user.GetName(), b.user.GetName()) })

	return userMatches{users: matches, attrs: attrs}, nil
}

// matchOwnUser matches query against the caller's own user, for credentials that cannot list users.
func (t *Tools) matchOwnUser(ctx context.Context, query string) (userMatches, error) {
	own, err := t.ownUser(ctx)
	if err != nil {
		return userMatches{}, err
	}

	attrs, err := t.loadUserAttribute(ctx, own.GetName())
	if err != nil {
		return userMatches{}, err
	}

	matchedOn, ok := matchUser(own, attrs.providerUsernames(own.GetName()), query)
	if !ok {
		return userMatches{}, fmt.Errorf("the credentials are not allowed to look up other users, only their own user %q", own.GetName())
	}

	return userMatches{users: []matchedUser{{user: own, matchedOn: matchedOn}}, attrs: attrs, ownOnly: true}, nil
}

// matchUser reports whether user's ID, login name, display name, principal ID or one of providerUsernames
// matches query, and which. Display names and provider usernames are matched case-insensitively,
// everything else exactly.
func matchUser(user *unstructured.Unstructured, providerUsernames map[string]string, query string) (string, bool) {
	switch {
	case user.GetName() == query:
		return matchedOnUserID, true
	case stringField(user.Object, "username") == query:
		return matchedOnLoginName, true
	case displayNameMatches(stringField(user.Object, "displayName"), query):
		return matchedOnDisplayName, true
	case slices.Contains(principalIDs(user), query):
		return matchedOnPrincipalID, true
	case providerUsernameMatches(providerUsernames, query):
		return matchedOnProviderUsername, true
	default:
		return "", false
	}
}

func displayNameMatches(displayName, query string) bool {
	return displayName != "" && strings.EqualFold(displayName, query)
}

func principalIDs(user *unstructured.Unstructured) []string {
	ids, _, _ := unstructured.NestedStringSlice(user.Object, "principalIds")

	return ids
}

// summarizeUser returns the summary of user, apart from the fields that need other resources.
func summarizeUser(user *unstructured.Unstructured, now time.Time) userSummary {
	summary := userSummary{
		UserID:       user.GetName(),
		LoginName:    stringField(user.Object, "username"),
		DisplayName:  stringField(user.Object, "displayName"),
		Description:  stringField(user.Object, "description"),
		PrincipalIDs: principalIDs(user),
		Enabled:      enabledField(user.Object, "enabled"),
	}

	if created := user.GetCreationTimestamp(); !created.IsZero() {
		summary.CreatedAt = created.UTC().Format(time.RFC3339)
	}

	summary.SystemAccount = hasSystemPrincipal(summary.PrincipalIDs)
	summary.AuthProviders = authProvidersForUser(summary.LoginName, summary.PrincipalIDs)
	summary.LastLogin = userLastLogin(user, summary.SystemAccount, now)

	return summary
}

// authProvidersForUser returns the providers a user can log in with, external providers first.
//
// Every user has a local principal, but only users with a login name have a local password: users
// created by an external provider when they first log in have none. So local is only included for
// users with a login name. Each external principal is a provider the user can log in with.
// Whether a provider is currently enabled is not checked.
func authProvidersForUser(loginName string, principalIDs []string) []string {
	providers := []string{}
	hasLocal := false

	for _, principalID := range principalIDs {
		switch provider := providerFromPrincipalID(principalID); provider {
		case "", "system":
		case "local":
			hasLocal = true
		default:
			if !slices.Contains(providers, provider) {
				providers = append(providers, provider)
			}
		}
	}

	if hasLocal && loginName != "" {
		providers = append(providers, "local")
	}

	return providers
}

// userLastLogin returns when user last logged in, from the last-login label.
func userLastLogin(user *unstructured.Unstructured, systemAccount bool, now time.Time) lastLogin {
	if systemAccount {
		return lastLogin{State: lastLoginNotApplicable}
	}

	if _, found := user.GetLabels()[lastLoginLabel]; !found {
		return lastLogin{State: lastLoginNoneRecorded}
	}

	loginTime, ok := lastLoginTime(user)
	if !ok {
		return lastLogin{State: lastLoginUnknown}
	}

	return lastLogin{State: lastLoginRecorded, Time: loginTime.Format(time.RFC3339), DaysAgo: daysSince(loginTime, now)}
}

// lastLoginTime returns the time in the last-login label of user.
func lastLoginTime(user *unstructured.Unstructured) (time.Time, bool) {
	seconds, err := strconv.ParseInt(user.GetLabels()[lastLoginLabel], 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}

	return time.Unix(seconds, 0).UTC(), true
}

// resolveUser returns the only user whose ID, login name, display name, principal ID or provider username
// matches query, or an error if none or several do. A user ID always resolves to that user, even if another
// user has it as their login name, because matchUsers fetches the user by ID first.
func (t *Tools) resolveUser(ctx context.Context, query string) (*unstructured.Unstructured, error) {
	user, _, err := t.resolveUserWithAttributes(ctx, query)

	return user, err
}

// resolveUserWithAttributes is resolveUser, also returning the UserAttributes matchUsers loaded, so callers
// that need the user's groups do not have to fetch them again.
func (t *Tools) resolveUserWithAttributes(ctx context.Context, query string) (*unstructured.Unstructured, userAttributes, error) {
	found, err := t.matchUsers(ctx, query)
	if err != nil {
		return nil, userAttributes{}, err
	}

	matches, attrs := found.users, found.attrs

	switch len(matches) {
	case 0:
		return nil, userAttributes{}, fmt.Errorf("user %q not found, specify a username, user ID, display name, principal ID or provider username", query)
	case 1:
		return matches[0].user, attrs, nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.user.GetName())
		}

		return nil, userAttributes{}, fmt.Errorf("%d users match %q, specify one of the user IDs: %s", len(matches), query, strings.Join(ids, ", "))
	}
}

// resolveUserID returns the user ID (metadata.name) of the only user matching query, see resolveUser.
func (t *Tools) resolveUserID(ctx context.Context, query string) (string, error) {
	user, err := t.resolveUser(ctx, query)
	if err != nil {
		return "", err
	}

	return user.GetName(), nil
}
