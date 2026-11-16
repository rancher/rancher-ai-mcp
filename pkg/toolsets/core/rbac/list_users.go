package rbac

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapListUsers = zap.String("tool", "listUsers")

// limitedUsersNote explains a result that only includes the caller's own user.
const limitedUsersNote = "the credentials are not allowed to list users, so only the caller's own user is included and other users may exist"

type listUsersParams struct {
	IncludeSystemAccounts bool  `json:"includeSystemAccounts,omitempty" jsonschema:"include the accounts Rancher creates for itself, such as cluster agents. Defaults to false"`
	Limit                 int64 `json:"limit,omitempty" jsonschema:"maximum number of users to return, defaults to 100"`
	Offset                int64 `json:"offset,omitempty" jsonschema:"how many users to skip from the start of the full list before returning results. Defaults to 0. Use it together with limit to page through results: increase offset by limit for each next page. When more users are available, the response gives the exact offset to use for the next page"`
}

// listUsersResult is the response of listUsers.
type listUsersResult struct {
	Users []userSummary `json:"users"`
	// Total is the number of users in the full list, across all pages, not including excluded system accounts.
	Total int `json:"total"`
	// SystemAccountsExcluded is the number of system accounts left out of the list.
	SystemAccountsExcluded int    `json:"systemAccountsExcluded"`
	Offset                 int64  `json:"offset"`
	Limit                  int64  `json:"limit"`
	NextOffset             *int64 `json:"nextOffset,omitempty"`
	// Visibility is limited when the credentials are not allowed to list users, so only the caller's own user is included.
	Visibility        string   `json:"visibility"`
	UnavailableFields []string `json:"unavailableFields,omitempty"`
	Notes             []string `json:"notes,omitempty"`
}

// listUsers lists Rancher users as summaries, sorted by user ID.
//
// Users are listed through the Kubernetes API rather than Rancher's /v3 or Steve APIs, because those
// silently return only the caller's own user to users who are not allowed to list users. The Kubernetes
// API returns a 403 instead, so a limited result can be reported as limited.
func (t *Tools) listUsers(ctx context.Context, toolReq *mcp.CallToolRequest, params listUsersParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listUsers called")

	var (
		wg                           sync.WaitGroup
		users                        []*unstructured.Unstructured
		limited                      bool
		roles                        directGlobalRoles
		attrs                        userAttributes
		usersErr, rolesErr, attrsErr error
	)

	wg.Go(func() { users, limited, usersErr = t.listAllUsers(ctx) })
	wg.Go(func() { roles, rolesErr = t.loadDirectGlobalRoles(ctx) })
	wg.Go(func() { attrs, attrsErr = t.loadUserAttributes(ctx) })
	wg.Wait()

	if err := errors.Join(usersErr, rolesErr, attrsErr); err != nil {
		zap.L().Error("failed to list users", zapListUsers, zap.Error(err))

		return nil, nil, err
	}

	result := listUsersResult{Visibility: visibilityFull}
	if limited {
		result.Visibility = visibilityLimited
		result.Notes = append(result.Notes, limitedUsersNote)
	}

	if !params.IncludeSystemAccounts {
		users, result.SystemAccountsExcluded = withoutSystemAccounts(users)
	}

	slices.SortFunc(users, func(a, b *unstructured.Unstructured) int { return strings.Compare(a.GetName(), b.GetName()) })

	p := paginate(users, params.Offset, params.Limit)
	result.Total, result.Offset, result.Limit, result.NextOffset = len(users), p.offset, p.limit, p.next

	if p.next != nil {
		result.Notes = append(result.Notes, fmt.Sprintf("returned %d of %d users, to get the next page set offset=%d", len(p.items), len(users), *p.next))
	}

	now := t.now()
	result.Users = make([]userSummary, 0, len(p.items))
	userIDs := make([]string, 0, len(p.items))

	for _, u := range p.items {
		summary := describeUser(u, attrs, roles, now, summaryBrief)
		result.Users = append(result.Users, summary)
		userIDs = append(userIDs, summary.UserID)
	}

	if len(result.Users) > 0 {
		unavailable, notes := summaryLimits(attrs, roles, summaryBrief)
		result.UnavailableFields = append(result.UnavailableFields, unavailable...)
		result.Notes = append(result.Notes, notes...)
	}

	mcpResponse, err := response.CreateMcpResponseAny(result, userUIContext(userIDs...)...)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListUsers, zap.Error(err))

		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// listAllUsers returns every user. If the credentials are not allowed to list users, it returns only the
// caller's own user and reports that the result is limited.
func (t *Tools) listAllUsers(ctx context.Context) (users []*unstructured.Unstructured, limited bool, err error) {
	users, err = t.client.GetResources(ctx, client.ListParams{Cluster: "local", Kind: "user", Token: middleware.Token(ctx)})
	if !apierrors.IsForbidden(err) {
		return users, false, err
	}

	zap.L().Info("forbidden listing users, returning own user", zap.Error(err))

	own, err := t.ownUser(ctx)
	if err != nil {
		return nil, true, err
	}

	return []*unstructured.Unstructured{own}, true, nil
}

// withoutSystemAccounts returns users without system accounts, and how many were removed.
func withoutSystemAccounts(users []*unstructured.Unstructured) ([]*unstructured.Unstructured, int) {
	people := slices.DeleteFunc(slices.Clone(users), isSystemAccount)

	return people, len(users) - len(people)
}

// ownUser returns the caller's own user, which Rancher lets every user read.
func (t *Tools) ownUser(ctx context.Context) (*unstructured.Unstructured, error) {
	userInfo, err := t.reviewSelf(ctx)
	if err != nil {
		return nil, err
	}

	user, err := t.client.GetResource(ctx, client.GetParams{Cluster: "local", Kind: "user", Name: userInfo.Username, Token: middleware.Token(ctx)})
	if err != nil {
		return nil, fmt.Errorf("getting the caller's own user %q: %w", userInfo.Username, err)
	}

	return user, nil
}

// isSystemAccount reports whether user is an account Rancher created for itself, which has a system principal.
func isSystemAccount(user *unstructured.Unstructured) bool {
	return hasSystemPrincipal(principalIDs(user))
}
