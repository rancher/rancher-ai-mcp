package rbac

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
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

// serverMetadataFields are the metadata fields maintained by the API server and Rancher's controllers.
var serverMetadataFields = []string{"managedFields", "uid", "resourceVersion", "generation", "finalizers", "ownerReferences"}

// internalAnnotationPrefixes are the prefixes of annotations that Rancher's controllers use to
// track their own state.
var internalAnnotationPrefixes = []string{"lifecycle.cattle.io/", "authz.management.cattle.io/", "cleanup.cattle.io/"}

// removeServerManagedFields removes fields maintained by the server, so responses and plans
// only show what is relevant to the resource. The status is reduced by summarizeStatus.
func removeServerManagedFields(obj *unstructured.Unstructured) {
	for _, field := range serverMetadataFields {
		unstructured.RemoveNestedField(obj.Object, "metadata", field)
	}
	// The generated name is only needed to identify a resource that has not been named yet.
	if obj.GetName() != "" {
		unstructured.RemoveNestedField(obj.Object, "metadata", "generateName")
	}

	annotations := obj.GetAnnotations()
	for key := range annotations {
		for _, prefix := range internalAnnotationPrefixes {
			if strings.HasPrefix(key, prefix) {
				delete(annotations, key)
				break
			}
		}
	}

	obj.SetAnnotations(annotations)

	summarizeStatus(obj)
}

// summarizeStatus reduces the status of a resource to what can explain a problem with it.
// Conditions that are True are removed, as are timestamps and observed generations, so a
// resource that has been reconciled keeps only its summary.
func summarizeStatus(obj *unstructured.Unstructured) {
	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if err != nil || !found {
		return
	}

	for key, value := range status {
		switch {
		case key == "lastUpdateTime", strings.HasPrefix(key, "observedGeneration"):
			delete(status, key)
		case strings.HasSuffix(strings.ToLower(key), "conditions"):
			conditions, ok := value.([]any)
			if !ok {
				continue
			}

			var problems []any

			for _, condition := range conditions {
				if conditionMap, ok := condition.(map[string]any); ok && conditionMap["status"] == "True" {
					continue
				}

				problems = append(problems, condition)
			}

			if len(problems) == 0 {
				delete(status, key)
			} else {
				status[key] = problems
			}
		default:
			// Other status fields, such as the summary, are kept.
		}
	}

	if len(status) == 0 {
		unstructured.RemoveNestedField(obj.Object, "status")
		return
	}

	_ = unstructured.SetNestedMap(obj.Object, status, "status")
}

// createResourcesResponse creates an MCP response for resources in the local cluster, with
// server-managed fields removed.
func createResourcesResponse(objs []*unstructured.Unstructured, notes ...string) (string, error) {
	for _, obj := range objs {
		removeServerManagedFields(obj)
	}

	return response.CreateMcpResponse(objs, "local", notes...)
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
	user, err := t.resolveUser(ctx, nameOrID)
	if err != nil {
		return nil, err
	}

	subjects := &userSubjects{userID: user.GetName()}
	subjects.principalIDs, _, _ = unstructured.NestedStringSlice(user.Object, "principalIds")

	// Rancher records the groups a user belongs to in the UserAttribute with the same name as the user.
	// It is only created once the user logs in, so a missing UserAttribute means the user has no groups.
	userAttribute, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: "local",
		Kind:    "userattribute",
		Name:    subjects.userID,
		Token:   middleware.Token(ctx),
	})
	if apierrors.IsNotFound(err) {
		return subjects, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get groups for user %q: %w", subjects.userID, err)
	}

	// The GroupPrincipals and Items fields of UserAttribute have no JSON tags in Rancher's types, so they
	// are stored with capitalized keys. Each item is a Principal, with its principal ID in metadata.name.
	groupPrincipals, _, _ := unstructured.NestedMap(userAttribute.Object, "GroupPrincipals")
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

			if name, found, err := unstructured.NestedString(itemMap, "metadata", "name"); err == nil && found && name != "" {
				subjects.groups = append(subjects.groups, name)
			}
		}
	}

	slices.Sort(subjects.groups)

	subjects.groups = slices.Compact(subjects.groups)
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
