package rbac

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var errUserAndGroup = errors.New("only one of user or group can be specified, bindings for the user's groups are included when a user is specified")

func filterResourcesByField(resources []*unstructured.Unstructured, field, value string) []*unstructured.Unstructured {
	var filteredResources []*unstructured.Unstructured

	for _, resource := range resources {
		resourceValue, found, err := unstructured.NestedString(resource.Object, field)
		if err == nil && found && resourceValue == value {
			filteredResources = append(filteredResources, resource)
		}
	}

	return filteredResources
}

// grbSubjectFields are the fields of a global role binding that record who it assigns the global role to.
var grbSubjectFields = []string{"userName", "userPrincipalName", "groupPrincipalName"}

// summarizeGlobalRoleBinding returns the name of a global role binding and who it assigns the global
// role to, with the global role if includeGlobalRole is true.
func summarizeGlobalRoleBinding(grb *unstructured.Unstructured, includeGlobalRole bool) map[string]any {
	fields := grbSubjectFields
	if includeGlobalRole {
		fields = append([]string{"globalRoleName"}, fields...)
	}

	summary := map[string]any{"name": grb.GetName()}
	for _, field := range fields {
		if value, found, err := unstructured.NestedString(grb.Object, field); err == nil && found && value != "" {
			summary[field] = value
		}
	}

	return summary
}

// userSubjects identifies a user, and the groups they belong to, in the subjects of bindings.
type userSubjects struct {
	// userID is the name of the user resource, used in the userName field of bindings.
	userID string
	// principalIDs are the principals of the user, used in the userPrincipalName field of bindings.
	principalIDs []string
	// groups are the principal names of the groups the user belongs to, used in the
	// groupPrincipalName field of bindings.
	groups []string
	// groupNames are the Kubernetes group names of the groups the user belongs to, used in the
	// groupName field of bindings. See rbacGroupName.
	groupNames []string
}

// rbacGroupName returns the Kubernetes group name that Rancher authenticates a user with for a group
// principal. Rancher removes the "local://" prefix, so the local group principal local://g-abc is the
// group g-abc, which is also the name of the Rancher Group resource used in the groupName field.
func rbacGroupName(groupPrincipal string) string {
	return strings.TrimPrefix(groupPrincipal, "local://")
}

// getUserSubjects resolves a username or user ID to the subjects that bindings for the user, and the
// groups the user belongs to, are made for.
func (t *Tools) getUserSubjects(ctx context.Context, nameOrID string) (*userSubjects, error) {
	user, attrs, err := t.resolveUserWithAttributes(ctx, nameOrID)
	if err != nil {
		return nil, err
	}

	subjects := &userSubjects{userID: user.GetName()}
	subjects.principalIDs, _, _ = unstructured.NestedStringSlice(user.Object, "principalIds")

	// Bindings for the user's groups cannot be found without their groups, so leaving them out silently
	// would understate the user's access.
	if attrs.unavailable {
		return nil, fmt.Errorf("failed to get groups for user %q: the credentials are not allowed to read user attributes", subjects.userID)
	}

	subjects.groups = attrs.groups(subjects.userID)
	for _, group := range subjects.groups {
		subjects.groupNames = append(subjects.groupNames, rbacGroupName(group))
	}

	return subjects, nil
}

// filterBindingsForUser returns the bindings for the user, or for any of the groups the user belongs to.
// Each binding is returned at most once.
func filterBindingsForUser(bindings []*unstructured.Unstructured, subjects *userSubjects) []*unstructured.Unstructured {
	var filtered []*unstructured.Unstructured

	for _, binding := range bindings {
		if bindingMatchesUser(binding, subjects) {
			filtered = append(filtered, binding)
		}
	}

	return filtered
}

func bindingMatchesUser(binding *unstructured.Unstructured, subjects *userSubjects) bool {
	if userName, found, err := unstructured.NestedString(binding.Object, "userName"); err == nil && found && userName == subjects.userID {
		return true
	}

	if principal, found, err := unstructured.NestedString(binding.Object, "userPrincipalName"); err == nil && found && slices.Contains(subjects.principalIDs, principal) {
		return true
	}

	if group, found, err := unstructured.NestedString(binding.Object, "groupPrincipalName"); err == nil && found && slices.Contains(subjects.groups, group) {
		return true
	}

	if group, found, err := unstructured.NestedString(binding.Object, "groupName"); err == nil && found && slices.Contains(subjects.groupNames, group) {
		return true
	}

	return false
}

// filterBindingsForGroup returns the bindings for the group principal. Bindings normally record the
// group in groupPrincipalName, but cluster and project role template bindings can also record its
// Kubernetes group name in groupName.
func filterBindingsForGroup(bindings []*unstructured.Unstructured, group string) []*unstructured.Unstructured {
	var filtered []*unstructured.Unstructured

	for _, binding := range bindings {
		if value, found, err := unstructured.NestedString(binding.Object, "groupPrincipalName"); err == nil && found && value == group {
			filtered = append(filtered, binding)
			continue
		}

		if value, found, err := unstructured.NestedString(binding.Object, "groupName"); err == nil && found && value == rbacGroupName(group) {
			filtered = append(filtered, binding)
		}
	}

	return filtered
}

// userGroupsNote describes the groups whose bindings were included for a user, if any.
func userGroupsNote(groups []string) []string {
	if len(groups) == 0 {
		return nil
	}

	return []string{"includes bindings for the groups the user belongs to: " + strings.Join(groups, ", ")}
}
