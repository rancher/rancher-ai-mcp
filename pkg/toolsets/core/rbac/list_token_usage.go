package rbac

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapListTokenUsage = zap.String("tool", "listTokenUsage")

// Rancher stores tokens in two places. The token kinds are deliberately not added to the converter, so the
// generic resource tools cannot return them with their secret values.
var (
	// v3TokensGVR are the original tokens, used for UI sessions and API keys.
	v3TokensGVR = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "tokens"}
	// extTokensGVR are the newer tokens served by Rancher's extension API server, e.g. kubeconfig tokens.
	extTokensGVR = schema.GroupVersionResource{Group: "ext.cattle.io", Version: "v1", Resource: "tokens"}
)

const (
	// v3TokensPath lists the caller's own v3 tokens, without their values, for users who cannot list tokens.
	v3TokensPath = "/v3/tokens"
	// maxV3TokenPages bounds how many pages of /v3/tokens are followed, in case the next links loop.
	maxV3TokenPages = 100
	// tokenKindLabel is set on v3 tokens to their kind, e.g. session or kubeconfig.
	tokenKindLabel = "authn.management.cattle.io/kind"
)

// The stores a token comes from, in tokenUsage.Store.
const (
	tokenStoreV3  = "v3"
	tokenStoreExt = "ext"
)

// The types of token, in tokenUsage.Type, unless Rancher reports another kind.
const (
	tokenTypeSession = "session"
	tokenTypeAPIKey  = "apiKey"
)

const (
	ownTokensOnlyNote = "the credentials are not allowed to list every user's tokens, so only the caller's own tokens are included"
	noExtTokensNote   = "the newer ext.cattle.io tokens, such as kubeconfig tokens, could not be listed, so they are not included"
	ownExtTokensNote  = "the credentials can list every user's v3 tokens but not every user's ext.cattle.io tokens, such as kubeconfig tokens, so only the caller's own ext tokens are included"
)

type listTokenUsageParams struct {
	User           string `json:"user,omitempty" jsonschema:"only report the tokens of this user, given as a username or user ID, or a display name or principal ID. Defaults to every user"`
	IncludeExpired bool   `json:"includeExpired,omitempty" jsonschema:"also list expired tokens. They are always counted. Defaults to false"`
	Limit          int64  `json:"limit,omitempty" jsonschema:"maximum number of users to return, defaults to 100"`
	Offset         int64  `json:"offset,omitempty" jsonschema:"how many users to skip before returning results. Defaults to 0. Increase offset by limit for each next page. When more users are available, the response gives the exact offset to use for the next page"`
}

// tokenUsage is a token and when it was last used. It never includes the token's value.
type tokenUsage struct {
	Name  string `json:"name"`
	Store string `json:"store"`
	// Type is session for tokens created by logging in, apiKey for API keys, or another kind such as kubeconfig.
	Type         string `json:"type"`
	Description  string `json:"description,omitempty"`
	AuthProvider string `json:"authProvider,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	// ExpiresAt is empty for tokens that never expire.
	ExpiresAt  string `json:"expiresAt,omitempty"`
	Expired    bool   `json:"expired"`
	Enabled    bool   `json:"enabled"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	// DaysSinceUse is nil if the token has never been used.
	DaysSinceUse *int `json:"daysSinceUse,omitempty"`
	// Current is true for the token used to make this request. It is only known for the caller's own v3 tokens.
	Current bool `json:"current,omitempty"`

	userID   string
	lastUsed time.Time
}

// userTokenUsage is the token usage of one user.
type userTokenUsage struct {
	UserID      string `json:"userId"`
	LoginName   string `json:"loginName,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	// ActiveTokens is the number of enabled tokens that have not expired.
	ActiveTokens   int `json:"activeTokens"`
	ExpiredTokens  int `json:"expiredTokens"`
	DisabledTokens int `json:"disabledTokens"`
	// LastUsedAt is the most recent use of any of the user's tokens, including tokens that have since expired.
	LastUsedAt       string       `json:"lastUsedAt,omitempty"`
	DaysSinceLastUse *int         `json:"daysSinceLastUse,omitempty"`
	Tokens           []tokenUsage `json:"tokens"`

	lastUsed time.Time
}

type tokenUsageCounts struct {
	UsersWithTokens int `json:"usersWithTokens"`
	Tokens          int `json:"tokens"`
	ActiveTokens    int `json:"activeTokens"`
	ExpiredTokens   int `json:"expiredTokens"`
	// DisabledTokens is the number of tokens that have not expired but are disabled.
	DisabledTokens int `json:"disabledTokens"`
	// NeverUsedTokens is the number of active tokens that have never been used.
	NeverUsedTokens int `json:"neverUsedTokens"`
}

// listTokenUsageResult is the response of listTokenUsage.
type listTokenUsageResult struct {
	EvaluatedAt string           `json:"evaluatedAt"`
	Counts      tokenUsageCounts `json:"counts"`
	Users       []userTokenUsage `json:"users"`
	Offset      int64            `json:"offset"`
	Limit       int64            `json:"limit"`
	NextOffset  *int64           `json:"nextOffset,omitempty"`
	// Visibility is limited when the credentials can only see their own tokens, or a token store could not be read.
	Visibility string   `json:"visibility"`
	Notes      []string `json:"notes,omitempty"`
}

// tokenList is the tokens from both stores, with notes about anything that could not be read.
type tokenList struct {
	tokens []tokenUsage
	notes  []string
	// limited is true when only the caller's own tokens, or only one store, could be read.
	limited bool
}

// listTokenUsage reports the tokens each user has and when they were last used.
//
// Admins see every user's tokens through the Kubernetes API. Other users are not allowed to list v3 tokens
// there, so their own v3 tokens are read from Rancher's /v3 API instead, which only returns the caller's
// tokens. The ext token API only returns the caller's own tokens to users who cannot see all of them.
func (t *Tools) listTokenUsage(ctx context.Context, toolReq *mcp.CallToolRequest, params listTokenUsageParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listTokenUsage called")

	now := t.now().UTC()

	var (
		wg                  sync.WaitGroup
		filterUserID        string
		tokens              tokenList
		users               []*unstructured.Unstructured
		filterErr, tokenErr error
	)

	if params.User != "" {
		wg.Go(func() { filterUserID, filterErr = t.resolveUserID(ctx, params.User) })
	}

	wg.Go(func() { tokens, tokenErr = t.listAllTokens(ctx, now) })
	wg.Go(func() {
		// Names are only a convenience, so users who cannot be listed are not an error.
		var err error
		if users, _, err = t.listAllUsers(ctx); err != nil {
			zap.L().Info("cannot list users to name token owners", zapListTokenUsage, zap.Error(err))
		}
	})
	wg.Wait()

	if err := errors.Join(filterErr, tokenErr); err != nil {
		zap.L().Error("failed to list token usage", zapListTokenUsage, zap.Error(err))

		return nil, nil, err
	}

	result := listTokenUsageResult{EvaluatedAt: now.Format(time.RFC3339), Visibility: visibilityFull, Notes: tokens.notes}
	if tokens.limited {
		result.Visibility = visibilityLimited
	}

	byUser := map[string]*userTokenUsage{}

	for _, token := range tokens.tokens {
		// Tokens that do not belong to a user cannot be reported by user.
		if token.userID == "" || (filterUserID != "" && token.userID != filterUserID) {
			continue
		}

		usage, ok := byUser[token.userID]
		if !ok {
			usage = &userTokenUsage{UserID: token.userID, Tokens: []tokenUsage{}}
			byUser[token.userID] = usage
		}

		result.Counts.Tokens++
		// Using a token that has since expired is still activity.
		usage.lastUsed = latest(usage.lastUsed, token.lastUsed)

		switch {
		case token.Expired:
			usage.ExpiredTokens++
			result.Counts.ExpiredTokens++

			if !params.IncludeExpired {
				continue
			}
		case !token.Enabled:
			usage.DisabledTokens++
			result.Counts.DisabledTokens++
		default:
			usage.ActiveTokens++
			result.Counts.ActiveTokens++

			if token.lastUsed.IsZero() {
				result.Counts.NeverUsedTokens++
			}
		}

		usage.Tokens = append(usage.Tokens, token)
	}

	for _, u := range users {
		if usage, ok := byUser[u.GetName()]; ok {
			usage.LoginName = stringField(u.Object, "username")
			usage.DisplayName = stringField(u.Object, "displayName")
		}
	}

	userUsage := make([]userTokenUsage, 0, len(byUser))

	for _, usage := range byUser {
		if !usage.lastUsed.IsZero() {
			usage.LastUsedAt = usage.lastUsed.Format(time.RFC3339)
			usage.DaysSinceLastUse = daysSince(usage.lastUsed, now)
		}

		slices.SortFunc(usage.Tokens, func(a, b tokenUsage) int {
			return cmp.Or(b.lastUsed.Compare(a.lastUsed), cmp.Compare(a.Name, b.Name))
		})

		userUsage = append(userUsage, *usage)
	}

	// Most recently used first, then users whose tokens have never been used.
	slices.SortFunc(userUsage, func(a, b userTokenUsage) int {
		return cmp.Or(b.lastUsed.Compare(a.lastUsed), cmp.Compare(a.UserID, b.UserID))
	})

	result.Counts.UsersWithTokens = len(userUsage)
	p := paginate(userUsage, params.Offset, params.Limit)
	result.Users, result.Offset, result.Limit, result.NextOffset = p.items, p.offset, p.limit, p.next

	if p.next != nil {
		result.Notes = append(result.Notes, fmt.Sprintf("returned %d of %d users, to get the next page set offset=%d", len(p.items), len(userUsage), *p.next))
	}

	userIDs := make([]string, 0, len(result.Users))
	for _, u := range result.Users {
		userIDs = append(userIDs, u.UserID)
	}

	mcpResponse, err := response.CreateMcpResponseAny(result, userUIContext(userIDs...)...)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListTokenUsage, zap.Error(err))

		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// listAllTokens returns the tokens from both stores, read in parallel.
//
// Rancher's ext token API never refuses a list: callers without full access get a 200 with only their own
// tokens. So whether the ext list is complete is checked separately, with the same permission Rancher
// checks, see hasFullExtTokenAccess.
func (t *Tools) listAllTokens(ctx context.Context, now time.Time) (tokenList, error) {
	var (
		wg                    sync.WaitGroup
		list                  tokenList
		v3, ext               []tokenUsage
		v3Err, extErr         error
		ownV3, extUnavailable bool
		fullExtAccess         bool
	)

	wg.Go(func() {
		v3, v3Err = t.listTokens(ctx, v3TokensGVR, func(u *unstructured.Unstructured) tokenUsage { return v3TokenUsage(u, now) })
		if apierrors.IsForbidden(v3Err) {
			zap.L().Info("forbidden listing v3 tokens, listing own tokens", zapListTokenUsage, zap.Error(v3Err))

			ownV3 = true
			v3, v3Err = t.ownV3Tokens(ctx, now)
		}
	})
	wg.Go(func() {
		ext, extErr = t.listTokens(ctx, extTokensGVR, func(u *unstructured.Unstructured) tokenUsage { return extTokenUsage(u, now) })
		if apierrors.IsForbidden(extErr) || apierrors.IsNotFound(extErr) {
			zap.L().Info("cannot list ext tokens", zapListTokenUsage, zap.Error(extErr))

			extUnavailable, extErr = true, nil
		}
	})
	wg.Go(func() { fullExtAccess = t.hasFullExtTokenAccess(ctx) })
	wg.Wait()

	if err := errors.Join(v3Err, extErr); err != nil {
		return tokenList{}, err
	}

	list.tokens = append(v3, ext...)

	if ownV3 {
		list.limited = true
		list.notes = append(list.notes, ownTokensOnlyNote)
	}

	switch {
	case extUnavailable:
		list.limited = true
		list.notes = append(list.notes, noExtTokensNote)
	case !fullExtAccess && !ownV3:
		// Only needs saying when the v3 tokens are complete: otherwise ownTokensOnlyNote already covers it.
		list.limited = true
		list.notes = append(list.notes, ownExtTokensNote)
	default:
	}

	return list, nil
}

// hasFullExtTokenAccess reports whether Rancher's ext token API returns every user's tokens to the caller.
// Rancher grants that to callers allowed to list every resource ("*") in the local cluster, and silently
// filters the list to the caller's own tokens otherwise. This review deliberately matches Rancher's own
// check, which authorizes the verb on Resource "*" with no API group rather than on ext.cattle.io tokens:
// see UserName in rancher/pkg/ext/stores/tokens/tokens.go. If the check itself fails, the list is treated as
// filtered, so a result is never reported as complete without knowing.
func (t *Tools) hasFullExtTokenAccess(ctx context.Context) bool {
	clientset, err := t.client.CreateClientSet(ctx, middleware.Token(ctx), "local")
	if err != nil {
		zap.L().Info("cannot check ext token access", zapListTokenUsage, zap.Error(err))

		return false
	}

	review, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{Verb: "list", Resource: "*"},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		zap.L().Info("cannot check ext token access", zapListTokenUsage, zap.Error(err))

		return false
	}

	return review.Status.Allowed
}

func (t *Tools) listTokens(ctx context.Context, gvr schema.GroupVersionResource, convert func(*unstructured.Unstructured) tokenUsage) ([]tokenUsage, error) {
	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), "", "local", gvr)
	if err != nil {
		return nil, err
	}

	list, err := resourceInterface.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	tokens := make([]tokenUsage, 0, len(list.Items))
	for i := range list.Items {
		tokens = append(tokens, convert(&list.Items[i]))
	}

	return tokens, nil
}

// v3TokenCollection is a page of Rancher's /v3/tokens collection.
type v3TokenCollection struct {
	Data       []map[string]any `json:"data"`
	Pagination struct {
		Next string `json:"next"`
	} `json:"pagination"`
}

// ownV3Tokens returns the caller's own v3 tokens from Rancher's /v3 API, which never returns token values.
// It follows the collection's next links, so a long list is not silently cut short.
func (t *Tools) ownV3Tokens(ctx context.Context, now time.Time) ([]tokenUsage, error) {
	var tokens []tokenUsage

	path := v3TokensPath

	for range maxV3TokenPages {
		var collection v3TokenCollection
		if err := t.client.GetRancherURL(ctx, middleware.Token(ctx), http.MethodGet, path, nil, &collection); err != nil {
			return nil, fmt.Errorf("listing the caller's own tokens: %w", err)
		}

		for _, data := range collection.Data {
			tokens = append(tokens, v3TokenUsageFromAPI(data, now))
		}

		if collection.Pagination.Next == "" {
			return tokens, nil
		}

		next, err := url.Parse(collection.Pagination.Next)
		if err != nil {
			return nil, fmt.Errorf("following the next page of the caller's own tokens: %w", err)
		}

		path = next.RequestURI()
	}

	return nil, fmt.Errorf("listing the caller's own tokens: more than %d pages", maxV3TokenPages)
}

// v3TokenUsageFromAPI converts a token from Rancher's /v3 API, which has the same fields as the resource
// except the name is in id, the creation time in created, and labels at the top level.
func v3TokenUsageFromAPI(data map[string]any, now time.Time) tokenUsage {
	u := &unstructured.Unstructured{Object: data}
	u.SetName(stringField(data, "id"))

	if created, err := time.Parse(time.RFC3339, stringField(data, "created")); err == nil {
		u.SetCreationTimestamp(metav1.NewTime(created))
	}

	if labels, ok := data["labels"].(map[string]any); ok {
		stringLabels := make(map[string]string, len(labels))

		for k, v := range labels {
			if s, ok := v.(string); ok {
				stringLabels[k] = s
			}
		}

		u.SetLabels(stringLabels)
	}

	token := v3TokenUsage(u, now)
	token.Current, _, _ = unstructured.NestedBool(data, "current")

	return token
}

// v3TokenUsage converts a management.cattle.io/v3 Token.
func v3TokenUsage(u *unstructured.Unstructured, now time.Time) tokenUsage {
	token := tokenUsage{
		Name:         u.GetName(),
		Store:        tokenStoreV3,
		Type:         u.GetLabels()[tokenKindLabel],
		Description:  stringField(u.Object, "description"),
		AuthProvider: stringField(u.Object, "authProvider"),
		userID:       stringField(u.Object, "userId"),
	}

	if token.Type == "" {
		token.Type = tokenTypeSession
		if derived, _, _ := unstructured.NestedBool(u.Object, "isDerived"); derived {
			token.Type = tokenTypeAPIKey
		}
	}

	expired, _, _ := unstructured.NestedBool(u.Object, "expired")
	token.setState(u, tokenState{
		expiresAt:  stringField(u.Object, "expiresAt"),
		lastUsedAt: stringField(u.Object, "lastUsedAt"),
		expired:    expired,
		enabled:    enabledField(u.Object, "enabled"),
	}, now)

	return token
}

// extTokenUsage converts an ext.cattle.io/v1 Token.
func extTokenUsage(u *unstructured.Unstructured, now time.Time) tokenUsage {
	token := tokenUsage{
		Name:         u.GetName(),
		Store:        tokenStoreExt,
		Type:         cmp.Or(stringField(u.Object, "spec", "kind"), tokenTypeAPIKey),
		Description:  stringField(u.Object, "spec", "description"),
		AuthProvider: stringField(u.Object, "status", "authProvider"),
		userID:       stringField(u.Object, "spec", "userID"),
	}

	expired, _, _ := unstructured.NestedBool(u.Object, "status", "expired")
	token.setState(u, tokenState{
		expiresAt:  stringField(u.Object, "status", "expiresAt"),
		lastUsedAt: stringField(u.Object, "status", "lastUsedAt"),
		expired:    expired,
		enabled:    enabledField(u.Object, "spec", "enabled"),
	}, now)

	return token
}

// tokenState is the expiry and use of a token, as stored in either store.
type tokenState struct {
	expiresAt, lastUsedAt string
	expired, enabled      bool
}

// setState sets the creation, expiry, enabled state and last use of the token.
func (token *tokenUsage) setState(u *unstructured.Unstructured, state tokenState, now time.Time) {
	if created := u.GetCreationTimestamp(); !created.IsZero() {
		token.CreatedAt = created.UTC().Format(time.RFC3339)
	}

	token.Expired, token.Enabled = state.expired, state.enabled

	if expiry, err := time.Parse(time.RFC3339, state.expiresAt); err == nil {
		token.ExpiresAt = expiry.UTC().Format(time.RFC3339)
		token.Expired = token.Expired || !expiry.After(now)
	}

	if lastUsed, err := time.Parse(time.RFC3339, state.lastUsedAt); err == nil {
		token.lastUsed = lastUsed.UTC()
		token.LastUsedAt = token.lastUsed.Format(time.RFC3339)
		token.DaysSinceUse = daysSince(token.lastUsed, now)
	}
}
