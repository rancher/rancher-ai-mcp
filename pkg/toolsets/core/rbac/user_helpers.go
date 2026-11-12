package rbac

import (
	"cmp"
	"context"
	"slices"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

// defaultPageLimit is the page size of the user tools when no limit is given.
const defaultPageLimit int64 = 100

// userSteveType is the type the UI uses to link to a User.
const userSteveType = "management.cattle.io.user"

// The visibility of a result.
const (
	// visibilityFull means everything the tool reports on could be read.
	visibilityFull = "full"
	// visibilityLimited means the credentials could not read everything, so the result is incomplete.
	visibilityLimited = "limited"
)

// page is a window of a list, and the offset of the next window if there is one.
type page[T any] struct {
	items  []T
	offset int64
	limit  int64
	next   *int64
}

// paginate returns the window of items starting at offset, of at most limit items. A limit of zero or
// less means defaultPageLimit, and a negative offset means zero.
func paginate[T any](items []T, offset, limit int64) page[T] {
	if limit <= 0 {
		limit = defaultPageLimit
	}

	offset = max(offset, 0)
	total := int64(len(items))
	// end is computed from what is left after start, since offset+limit can overflow.
	start := min(offset, total)
	end := start + min(limit, total-start)
	p := page[T]{items: items[start:end], offset: offset, limit: limit}

	if end < total {
		p.next = new(end)
	}

	return p
}

// userUIContext returns the UI context for linking to each user.
func userUIContext(userIDs ...string) []response.UIContext {
	uiContext := make([]response.UIContext, 0, len(userIDs))
	for _, id := range userIDs {
		uiContext = append(uiContext, response.UIContext{Kind: "User", Cluster: "local", Name: id, Type: userSteveType})
	}

	return uiContext
}

// directGlobalRoles are the global roles bound directly to users, by user ID or user principal, and to
// group principals, or unavailable if the credentials are not allowed to list global role bindings.
type directGlobalRoles struct {
	byUser      map[string][]string
	byPrincipal map[string][]string
	byGroup     map[string][]string
	unavailable bool
}

// groupGlobalRole is a global role a user has through a group they belong to.
type groupGlobalRole struct {
	GlobalRole string `json:"globalRole"`
	Group      string `json:"group"`
}

// loadDirectGlobalRoles lists the global role bindings. A 403 is not an error: the roles are reported as
// unavailable instead.
func (t *Tools) loadDirectGlobalRoles(ctx context.Context) (directGlobalRoles, error) {
	bindings, err := t.client.GetResources(ctx, client.ListParams{Cluster: "local", Kind: "globalrolebinding", Token: middleware.Token(ctx)})
	if apierrors.IsForbidden(err) {
		zap.L().Info("forbidden listing global role bindings", zap.Error(err))

		return directGlobalRoles{unavailable: true}, nil
	}

	if err != nil {
		return directGlobalRoles{}, err
	}

	roles := directGlobalRoles{byUser: map[string][]string{}, byPrincipal: map[string][]string{}, byGroup: map[string][]string{}}

	// A binding names its subject by user ID, by user principal, or by group principal, see bindingMatchesUser.
	for _, binding := range bindings {
		globalRole := stringField(binding.Object, "globalRoleName")
		if globalRole == "" {
			continue
		}

		addRole(roles.byUser, stringField(binding.Object, "userName"), globalRole)
		addRole(roles.byPrincipal, stringField(binding.Object, "userPrincipalName"), globalRole)
		addRole(roles.byGroup, stringField(binding.Object, "groupPrincipalName"), globalRole)
	}

	for _, index := range []map[string][]string{roles.byUser, roles.byPrincipal, roles.byGroup} {
		for _, r := range index {
			slices.Sort(r)
		}
	}

	return roles, nil
}

// addRole records that subject is bound to globalRole, unless subject is empty or already recorded.
func addRole(index map[string][]string, subject, globalRole string) {
	if subject != "" && !slices.Contains(index[subject], globalRole) {
		index[subject] = append(index[subject], globalRole)
	}
}

// forGroups returns the global roles bound to any of groups, sorted by role then group, or nil if there are
// none or they are unavailable.
func (r directGlobalRoles) forGroups(groups []string) []groupGlobalRole {
	var roles []groupGlobalRole

	for _, group := range groups {
		for _, role := range r.byGroup[group] {
			roles = append(roles, groupGlobalRole{GlobalRole: role, Group: group})
		}
	}

	slices.SortFunc(roles, func(a, b groupGlobalRole) int {
		return cmp.Or(cmp.Compare(a.GlobalRole, b.GlobalRole), cmp.Compare(a.Group, b.Group))
	})

	return roles
}

// forUser returns the global roles bound directly to the user, by their user ID or any of their principal
// IDs, sorted and without duplicates: nil if they are unavailable, so the field is omitted, or an empty
// slice if the user has none.
func (r directGlobalRoles) forUser(userID string, principalIDs []string) []string {
	if r.unavailable {
		return nil
	}

	roles := slices.Clone(r.byUser[userID])
	for _, principalID := range principalIDs {
		roles = append(roles, r.byPrincipal[principalID]...)
	}

	if len(roles) == 0 {
		return []string{}
	}

	slices.Sort(roles)

	return slices.Compact(roles)
}

// summaryDetail is how much of a user's UserAttribute a summary includes.
type summaryDetail int

const (
	// summaryBrief leaves out the user's groups, which can be many, for tools that return many users.
	summaryBrief summaryDetail = iota
	// summaryFull includes the user's groups.
	summaryFull
)

// describeUser returns the summary of user, with what their UserAttribute and the global role bindings say
// about them.
func describeUser(user *unstructured.Unstructured, attrs userAttributes, roles directGlobalRoles, now time.Time, detail summaryDetail) userSummary {
	summary := summarizeUser(user, now)
	summary.DirectGlobalRoles = roles.forUser(summary.UserID, summary.PrincipalIDs)
	summary.ProviderUsernames = attrs.providerUsernames(summary.UserID)

	groups := attrs.groups(summary.UserID)
	summary.GroupGlobalRoles = roles.forGroups(groups)

	if detail == summaryFull {
		summary.Groups = groups
	}

	return summary
}

// summaryLimits returns the unavailable fields and notes to report when user summaries are missing
// information the credentials are not allowed to read.
func summaryLimits(attrs userAttributes, roles directGlobalRoles, detail summaryDetail) (unavailableFields, notes []string) {
	if roles.unavailable {
		unavailableFields = append(unavailableFields, "directGlobalRoles", "groupGlobalRoles")
		notes = append(notes, "the credentials are not allowed to list global role bindings, so global roles are not included")
	}

	if attrs.unavailable {
		if detail == summaryFull {
			unavailableFields = append(unavailableFields, "groups")
		}

		if !roles.unavailable {
			unavailableFields = append(unavailableFields, "groupGlobalRoles")
		}

		unavailableFields = append(unavailableFields, "providerUsernames")
		notes = append(notes, "the credentials are not allowed to read user attributes, so the users' groups, global roles "+
			"through groups and provider usernames are not included, and users cannot be found by provider username")
	}

	return unavailableFields, notes
}

// daysSince returns the whole number of days from then to now, and never less than zero.
func daysSince(then, now time.Time) *int {
	return new(max(int(now.Sub(then).Hours()/24), 0))
}

// latest returns the later of a and b.
func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

func stringField(obj map[string]any, fields ...string) string {
	value, _, _ := unstructured.NestedString(obj, fields...)

	return value
}

// enabledField returns a boolean field that defaults to true when it is not set.
func enabledField(obj map[string]any, fields ...string) bool {
	if enabled, found, _ := unstructured.NestedBool(obj, fields...); found {
		return enabled
	}

	return true
}

// hasSystemPrincipal reports whether principalIDs belong to an account Rancher created for itself.
func hasSystemPrincipal(principalIDs []string) bool {
	return slices.ContainsFunc(principalIDs, func(p string) bool { return providerFromPrincipalID(p) == "system" })
}
