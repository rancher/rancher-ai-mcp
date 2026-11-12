package rbac

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapAuditInactiveUsers = zap.String("tool", "auditInactiveUsers")

// The reasons a user is reported by auditInactiveUsers, in auditUser.Reason.
const (
	reasonLastLoginBeforeCutoff = "lastLoginBeforeCutoff"
	reasonNeverLoggedIn         = "neverLoggedIn"
	reasonUndetermined          = "undetermined"
)

const (
	// apiTokenNote is included in audits without token activity, since token use is invisible to them.
	apiTokenNote = "Token activity was not included and using an API token does not count as a login, so a user or automation account that only uses API tokens is reported as inactive even if it is in use."
	// deletedTokensNote is included in audits with token activity, since deleted tokens leave no record of their use.
	deletedTokensNote = "Token use counts as activity, but deleted and cleaned-up tokens leave no record, so a user whose tokens have all been removed can still be reported as inactive."
	limitedAuditNote  = "the credentials are not allowed to list users, so only the caller's own user was audited and other users may be inactive"
)

// The outcomes of auditing a user.
type auditOutcome int

const (
	outcomeActive auditOutcome = iota
	// outcomeActiveByToken is a user who has not logged in since the cutoff, but has used a token since.
	outcomeActiveByToken
	outcomeRecentlyCreatedNoLogin
	outcomeUndetermined
	outcomeInactive
)

type auditInactiveUsersParams struct {
	InactiveDays         int   `json:"inactiveDays" jsonschema:"report users who have not logged in or used a token for at least this many days, e.g. 30 for a month or 90 for 3 months. Must be greater than 0"`
	IncludeTokenActivity *bool `json:"includeTokenActivity,omitempty" jsonschema:"count using an API key, kubeconfig or other token as activity. Defaults to true. Set to false to skip listing tokens on very large installations"`
	Limit                int64 `json:"limit,omitempty" jsonschema:"maximum number of inactive users to return, defaults to 100. Also limits how many undetermined users are listed"`
	Offset               int64 `json:"offset,omitempty" jsonschema:"how many inactive users to skip before returning results. Defaults to 0. Increase offset by limit for each next page. When more inactive users are available, the response gives the exact offset to use for the next page"`
}

// auditCounts are counted over every user checked, not only the returned page.
type auditCounts struct {
	// UsersChecked is the number of users audited, not including system accounts.
	UsersChecked int `json:"usersChecked"`
	Active       int `json:"active"`
	// ActiveByTokenUse is the number of active users who have not logged in since the cutoff but have used a
	// token since, included in Active.
	ActiveByTokenUse int `json:"activeByTokenUse"`
	Inactive         int `json:"inactive"`
	// NeverLoggedIn is the number of inactive users with no recorded login, included in Inactive.
	NeverLoggedIn int `json:"neverLoggedIn"`
	// AlreadyDisabled is the number of inactive users whose account is disabled, included in Inactive.
	AlreadyDisabled int `json:"alreadyDisabled"`
	// RecentlyCreatedNoLogin is the number of users with no recorded login who were created after the cutoff,
	// so have not existed long enough to be inactive. They are not included in Inactive.
	RecentlyCreatedNoLogin int `json:"recentlyCreatedNoLogin"`
	// Undetermined is the number of users whose last login could not be read and who have not used a token
	// since the cutoff.
	Undetermined           int `json:"undetermined"`
	SystemAccountsExcluded int `json:"systemAccountsExcluded"`
}

// auditUser is a user reported by the audit.
type auditUser struct {
	userSummary
	// DaysInactive is the number of days since the user's latest activity: their last login or token use, or
	// the account's creation if they have neither.
	DaysInactive *int `json:"daysInactive,omitempty"`
	// LastTokenUsedAt is the most recent use of any of the user's tokens, if token activity was included.
	LastTokenUsedAt string `json:"lastTokenUsedAt,omitempty"`
	Reason          string `json:"reason"`
	// lastActivity orders users, oldest first.
	lastActivity time.Time
}

// activeByTokenUser is a user who has not logged in since the cutoff but has used a token since. They are
// active, but have not logged in, so they answer "who has not logged in" even though they are not inactive.
type activeByTokenUser struct {
	UserID          string    `json:"userId"`
	LoginName       string    `json:"loginName,omitempty"`
	DisplayName     string    `json:"displayName,omitempty"`
	LastLogin       lastLogin `json:"lastLogin"`
	LastTokenUsedAt string    `json:"lastTokenUsedAt"`
}

// auditInactiveUsersResult is the response of auditInactiveUsers.
type auditInactiveUsersResult struct {
	InactiveDays          int         `json:"inactiveDays"`
	TokenActivityIncluded bool        `json:"tokenActivityIncluded"`
	Cutoff                string      `json:"cutoff"`
	EvaluatedAt           string      `json:"evaluatedAt"`
	Counts                auditCounts `json:"counts"`
	InactiveUsers         []auditUser `json:"inactiveUsers"`
	UndeterminedUsers     []auditUser `json:"undeterminedUsers,omitempty"`
	// ActiveByTokenUseUsers are the users counted in activeByTokenUse, sorted by user ID.
	ActiveByTokenUseUsers []activeByTokenUser `json:"activeByTokenUseUsers,omitempty"`
	Offset                int64               `json:"offset"`
	Limit                 int64               `json:"limit"`
	NextOffset            *int64              `json:"nextOffset,omitempty"`
	// Visibility is limited when the credentials could not list every user or every token.
	Visibility        string   `json:"visibility"`
	UnavailableFields []string `json:"unavailableFields,omitempty"`
	Notes             []string `json:"notes"`
}

// auditInactiveUsers reports the users who have not logged in or used a token since a cutoff.
//
// A user is active if they have logged in or used a token since the cutoff. Otherwise they are inactive if
// their last login is before the cutoff, or if they have never logged in and their account was created
// before the cutoff: users created after the cutoff have not existed long enough to be inactive. System
// accounts never log in, so they are not audited. Users, tokens and global roles are read in parallel.
func (t *Tools) auditInactiveUsers(ctx context.Context, toolReq *mcp.CallToolRequest, params auditInactiveUsersParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("auditInactiveUsers called")

	if params.InactiveDays <= 0 {
		return nil, nil, errors.New("inactiveDays must be greater than 0, e.g. 90 for 3 months")
	}

	now := t.now().UTC()
	cutoff := now.AddDate(0, 0, -params.InactiveDays)
	includeTokens := params.IncludeTokenActivity == nil || *params.IncludeTokenActivity

	var (
		wg                                      sync.WaitGroup
		users                                   []*unstructured.Unstructured
		usersLimited                            bool
		tokens                                  tokenList
		roles                                   directGlobalRoles
		attrs                                   userAttributes
		usersErr, tokensErr, rolesErr, attrsErr error
	)

	wg.Go(func() { users, usersLimited, usersErr = t.listAllUsers(ctx) })
	wg.Go(func() { roles, rolesErr = t.loadDirectGlobalRoles(ctx) })
	wg.Go(func() { attrs, attrsErr = t.loadUserAttributes(ctx) })

	if includeTokens {
		wg.Go(func() { tokens, tokensErr = t.listAllTokens(ctx, now) })
	}

	wg.Wait()

	if err := errors.Join(usersErr, tokensErr, rolesErr, attrsErr); err != nil {
		zap.L().Error("failed to audit users", zapAuditInactiveUsers, zap.Error(err))

		return nil, nil, err
	}

	result := auditInactiveUsersResult{
		InactiveDays:          params.InactiveDays,
		TokenActivityIncluded: includeTokens,
		Cutoff:                cutoff.Format(time.RFC3339),
		EvaluatedAt:           now.Format(time.RFC3339),
		Visibility:            visibilityFull,
	}

	if usersLimited {
		result.Visibility = visibilityLimited
		result.Notes = append(result.Notes, limitedAuditNote)
	}

	if tokens.limited {
		result.Visibility = visibilityLimited
	}

	result.Notes = append(result.Notes, tokens.notes...)

	users, result.Counts.SystemAccountsExcluded = withoutSystemAccounts(users)
	result.Counts.UsersChecked = len(users)
	tokenUse := lastTokenUse(tokens.tokens)

	// inactive is never nil, so an audit that finds nobody reports an empty list rather than null.
	inactive, undetermined, byToken := []auditUser{}, []auditUser(nil), []activeByTokenUser(nil)

	for _, u := range users {
		outcome, activity := classifyActivity(u, tokenUse[u.GetName()], cutoff)

		switch outcome {
		case outcomeActive:
			result.Counts.Active++
		case outcomeActiveByToken:
			result.Counts.Active++
			result.Counts.ActiveByTokenUse++

			byToken = append(byToken, newActiveByTokenUser(u, activity, now))
		case outcomeRecentlyCreatedNoLogin:
			result.Counts.RecentlyCreatedNoLogin++
		case outcomeUndetermined:
			result.Counts.Undetermined++

			undetermined = append(undetermined, newAuditUser(u, attrs, roles, activity, now))
		case outcomeInactive:
			audited := newAuditUser(u, attrs, roles, activity, now)
			result.Counts.Inactive++

			if audited.Reason == reasonNeverLoggedIn {
				result.Counts.NeverLoggedIn++
			}

			if !audited.Enabled {
				result.Counts.AlreadyDisabled++
			}

			inactive = append(inactive, audited)
		default:
		}
	}

	slices.SortFunc(inactive, func(a, b auditUser) int {
		return cmp.Or(a.lastActivity.Compare(b.lastActivity), cmp.Compare(a.UserID, b.UserID))
	})

	p := paginate(inactive, params.Offset, params.Limit)
	result.InactiveUsers, result.Offset, result.Limit, result.NextOffset = p.items, p.offset, p.limit, p.next

	if p.next != nil {
		result.Notes = append(result.Notes, fmt.Sprintf("returned %d of %d inactive users, to get the next page set offset=%d", len(p.items), len(inactive), *p.next))
	}

	slices.SortFunc(undetermined, func(a, b auditUser) int { return cmp.Compare(a.UserID, b.UserID) })

	result.UndeterminedUsers = paginate(undetermined, 0, p.limit).items
	if len(undetermined) > len(result.UndeterminedUsers) {
		result.Notes = append(result.Notes, fmt.Sprintf("listed %d of %d undetermined users", len(result.UndeterminedUsers), len(undetermined)))
	}

	slices.SortFunc(byToken, func(a, b activeByTokenUser) int { return cmp.Compare(a.UserID, b.UserID) })

	result.ActiveByTokenUseUsers = paginate(byToken, 0, p.limit).items
	if len(byToken) > len(result.ActiveByTokenUseUsers) {
		result.Notes = append(result.Notes, fmt.Sprintf("listed %d of %d users active only through token use", len(result.ActiveByTokenUseUsers), len(byToken)))
	}

	if len(result.InactiveUsers)+len(result.UndeterminedUsers) > 0 {
		unavailable, notes := summaryLimits(attrs, roles, summaryBrief)
		result.UnavailableFields = append(result.UnavailableFields, unavailable...)
		result.Notes = append(result.Notes, notes...)
	}

	if includeTokens {
		result.Notes = append(result.Notes, deletedTokensNote)
	} else {
		result.Notes = append(result.Notes, apiTokenNote)
	}

	userIDs := make([]string, 0, len(result.InactiveUsers)+len(result.UndeterminedUsers))
	for _, u := range slices.Concat(result.InactiveUsers, result.UndeterminedUsers) {
		userIDs = append(userIDs, u.UserID)
	}

	mcpResponse, err := response.CreateMcpResponseAny(result, userUIContext(userIDs...)...)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapAuditInactiveUsers, zap.Error(err))

		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// userActivity is what an audit found about a user it reports.
type userActivity struct {
	reason    string
	tokenUsed time.Time
	// latest is the user's latest activity: their last login or token use, or the account's creation.
	latest time.Time
}

// classifyActivity decides whether user has been active after cutoff, given when they last used a token
// (zero if never or unknown). Activity exactly at the cutoff is inactive: the user has then been inactive for
// exactly inactiveDays days, which satisfies "at least inactiveDays days". It only reads the last-login label
// and creation time, so users who are not reported are never summarized.
func classifyActivity(user *unstructured.Unstructured, tokenUsed, cutoff time.Time) (auditOutcome, userActivity) {
	activity := userActivity{tokenUsed: tokenUsed}
	loginTime, hasLogin := lastLoginTime(user)

	if hasLogin && loginTime.After(cutoff) {
		return outcomeActive, activity
	}

	if tokenUsed.After(cutoff) {
		return outcomeActiveByToken, activity
	}

	_, labelled := user.GetLabels()[lastLoginLabel]

	switch {
	case hasLogin:
		activity.reason = reasonLastLoginBeforeCutoff
		activity.latest = latest(loginTime, tokenUsed)
	case labelled:
		activity.reason = reasonUndetermined

		return outcomeUndetermined, activity
	default:
		created := user.GetCreationTimestamp().Time
		if created.After(cutoff) {
			return outcomeRecentlyCreatedNoLogin, activity
		}

		activity.reason = reasonNeverLoggedIn
		activity.latest = latest(created, tokenUsed)
	}

	return outcomeInactive, activity
}

// newAuditUser returns the reported form of a user the audit found inactive or undetermined.
func newAuditUser(user *unstructured.Unstructured, attrs userAttributes, roles directGlobalRoles, activity userActivity, now time.Time) auditUser {
	audited := auditUser{userSummary: describeUser(user, attrs, roles, now, summaryBrief), Reason: activity.reason, lastActivity: activity.latest}

	if !activity.tokenUsed.IsZero() {
		audited.LastTokenUsedAt = activity.tokenUsed.Format(time.RFC3339)
	}

	if !activity.latest.IsZero() {
		audited.DaysInactive = daysSince(activity.latest, now)
	}

	return audited
}

// newActiveByTokenUser returns the reported form of a user who is active only through token use.
func newActiveByTokenUser(user *unstructured.Unstructured, activity userActivity, now time.Time) activeByTokenUser {
	return activeByTokenUser{
		UserID:          user.GetName(),
		LoginName:       stringField(user.Object, "username"),
		DisplayName:     stringField(user.Object, "displayName"),
		LastLogin:       userLastLogin(user, false, now),
		LastTokenUsedAt: activity.tokenUsed.Format(time.RFC3339),
	}
}

// lastTokenUse returns when each user last used any of their tokens, by user ID.
func lastTokenUse(tokens []tokenUsage) map[string]time.Time {
	lastUse := map[string]time.Time{}
	for _, token := range tokens {
		lastUse[token.userID] = latest(lastUse[token.userID], token.lastUsed)
	}

	return lastUse
}
