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
		groups:       []string{"github_team://1234"},
	}
	binding := func(name string, fields map[string]any) *unstructured.Unstructured {
		return managementObject("ClusterRoleTemplateBinding", name, "local", fields)
	}

	tests := map[string]struct {
		binding  *unstructured.Unstructured
		expected bool
	}{
		"userName matches the user ID":                {binding("by-user-name", map[string]any{"userName": "u-user1"}), true},
		"userPrincipalName matches a user principal":  {binding("by-user-principal", map[string]any{"userPrincipalName": "github_user://99"}), true},
		"groupPrincipalName matches a group":          {binding("by-group-principal", map[string]any{"groupPrincipalName": "github_team://1234"}), true},
		"groupName matches a group":                   {binding("by-group-name", map[string]any{"groupName": "github_team://1234"}), true},
		"another user":                                {binding("other-user", map[string]any{"userName": "u-user2"}), false},
		"another user principal":                      {binding("other-user-principal", map[string]any{"userPrincipalName": "github_user://100"}), false},
		"another group":                               {binding("other-group", map[string]any{"groupPrincipalName": "github_team://5678"}), false},
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
