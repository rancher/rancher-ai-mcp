package rbac

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFilterBindingsForUser(t *testing.T) {
	subjects := &userSubjects{
		userID:       "u-user1",
		principalIDs: []string{"github_user://99", "local://u-user1"},
		groups:       []string{"github_team://1234", "local://g-abc"},
		groupNames:   []string{"github_team://1234", "g-abc"},
	}
	binding := func(name string, fields map[string]any) *unstructured.Unstructured {
		return managementObject("ClusterRoleTemplateBinding", name, "local", fields)
	}

	tests := map[string]struct {
		binding  *unstructured.Unstructured
		expected bool
	}{
		"userName matches the user ID":                             {binding("by-user-name", map[string]any{"userName": "u-user1"}), true},
		"userPrincipalName matches a user principal":               {binding("by-user-principal", map[string]any{"userPrincipalName": "github_user://99"}), true},
		"groupPrincipalName matches a group":                       {binding("by-group-principal", map[string]any{"groupPrincipalName": "github_team://1234"}), true},
		"groupName matches a group":                                {binding("by-group-name", map[string]any{"groupName": "github_team://1234"}), true},
		"groupPrincipalName matches a local group principal":       {binding("by-local-group-principal", map[string]any{"groupPrincipalName": "local://g-abc"}), true},
		"groupName matches a local group's group name":             {binding("by-local-group-name", map[string]any{"groupName": "g-abc"}), true},
		"groupName is not matched against a local group principal": {binding("local-principal-as-group-name", map[string]any{"groupName": "local://g-abc"}), false},
		"another user":           {binding("other-user", map[string]any{"userName": "u-user2"}), false},
		"another user principal": {binding("other-user-principal", map[string]any{"userPrincipalName": "github_user://100"}), false},
		"another group":          {binding("other-group", map[string]any{"groupPrincipalName": "github_team://5678"}), false},
		"user principal is not matched against group": {binding("principal-as-group", map[string]any{"groupPrincipalName": "github_user://99"}), false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			filtered := filterBindingsForUser([]*unstructured.Unstructured{tt.binding}, subjects)
			if tt.expected {
				assert.Equal(t, []*unstructured.Unstructured{tt.binding}, filtered)
			} else {
				assert.Empty(t, filtered)
			}
		})
	}

	t.Run("a binding matching several subjects is returned once", func(t *testing.T) {
		b := binding("many", map[string]any{
			"userName":           "u-user1",
			"userPrincipalName":  "local://u-user1",
			"groupPrincipalName": "github_team://1234",
			"groupName":          "github_team://1234",
		})
		assert.Equal(t, []*unstructured.Unstructured{b}, filterBindingsForUser([]*unstructured.Unstructured{b}, subjects))
	})
}

func TestFilterBindingsForGroup(t *testing.T) {
	bindings := []*unstructured.Unstructured{
		managementObject("ClusterRoleTemplateBinding", "by-principal", "local", map[string]any{"groupPrincipalName": "local://g-abc"}),
		managementObject("ClusterRoleTemplateBinding", "by-group-name", "local", map[string]any{"groupName": "g-abc"}),
		managementObject("ClusterRoleTemplateBinding", "other-group", "local", map[string]any{"groupName": "g-xyz"}),
	}

	var names []string
	for _, b := range filterBindingsForGroup(bindings, "local://g-abc") {
		names = append(names, b.GetName())
	}

	assert.Equal(t, []string{"by-principal", "by-group-name"}, names)
}

func TestRemoveServerManagedFields(t *testing.T) {
	grb := withServerFields(globalRoleBinding("grb-1", "admin", "userName", "u-user1"))

	removeServerManagedFields(grb)

	assert.Equal(t, map[string]any{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "GlobalRoleBinding",
		"metadata": map[string]any{
			"name":        "grb-1",
			"annotations": map[string]any{"field.cattle.io/creatorId": "user-sdxtj"},
		},
		"globalRoleName": "admin",
		"userName":       "u-user1",
		"status":         map[string]any{"summary": "Completed"},
	}, grb.Object)
}

func TestRemoveServerManagedFieldsKeepsGenerateNameWithoutName(t *testing.T) {
	grb := globalRoleBinding("", "admin", "userName", "u-user1")
	grb.SetGenerateName("grb-")

	removeServerManagedFields(grb)

	assert.Equal(t, "grb-", grb.GetGenerateName())
}

func TestSummarizeStatus(t *testing.T) {
	failed := map[string]any{"type": "SubjectReconciled", "status": "False", "reason": "SubjectNotFound", "message": "user not found"}
	tests := map[string]struct {
		status   map[string]any
		expected map[string]any
	}{
		"reconciled resource keeps only its summary": {
			status: map[string]any{
				"summary":        "Completed",
				"lastUpdateTime": "2026-11-04T14:38:09Z",
				"conditions":     []any{map[string]any{"type": "Ready", "status": "True"}},
			},
			expected: map[string]any{"summary": "Completed"},
		},
		"conditions that are not true are kept": {
			status: map[string]any{
				"summary":         "Error",
				"localConditions": []any{map[string]any{"type": "Ready", "status": "True"}, failed},
			},
			expected: map[string]any{"summary": "Error", "localConditions": []any{failed}},
		},
		"other status fields are kept": {
			status:   map[string]any{"backingNamespace": "p-abc", "observedGeneration": int64(1)},
			expected: map[string]any{"backingNamespace": "p-abc"},
		},
		"empty status is removed": {
			status:   map[string]any{"lastUpdateTime": "2026-11-04T14:38:09Z"},
			expected: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: map[string]any{"status": tt.status}}

			summarizeStatus(obj)

			if tt.expected == nil {
				assert.NotContains(t, obj.Object, "status")
				return
			}

			assert.Equal(t, tt.expected, obj.Object["status"])
		})
	}
}
