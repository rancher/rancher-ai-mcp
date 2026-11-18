package rbac

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
)

// principalIDSeparator separates the provider from the rest of a principal ID,
// e.g. openldap_group://cn=admins,ou=groups,dc=example,dc=org or local://g-abc.
const principalIDSeparator = "://"

// principal is a user or group principal as returned by Rancher's principal search.
type principal struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	LoginName     string `json:"loginName"`
	PrincipalType string `json:"principalType"`
}

// resolveGroupPrincipal returns the principal ID of the group identified by group, for the groupPrincipalName
// field of bindings. Rancher accepts any string there but only a principal ID, e.g. github_team://123, grants
// access, so a bare name such as "admins" would create a binding that silently grants nothing.
//
// A value that is already a principal ID is used as is. Anything else is looked up with Rancher's principal
// search, as the UI does when adding a member, and must match exactly one group.
func (t *Tools) resolveGroupPrincipal(ctx context.Context, group string) (string, error) {
	group = strings.TrimSpace(group)
	if strings.Contains(group, principalIDSeparator) {
		return group, nil
	}

	var result struct {
		Data []principal `json:"data"`
	}
	body := map[string]string{"name": group, "principalType": "group"}
	if err := t.client.GetRancherURL(ctx, middleware.Token(ctx), http.MethodPost, "/v3/principals?action=search", body, &result); err != nil {
		return "", fmt.Errorf("failed to search for group %q: %w", group, err)
	}

	// The search matches on part of the name, so only exact matches identify the group.
	var matches []string
	for _, p := range result.Data {
		if p.PrincipalType == "group" && strings.EqualFold(p.Name, group) {
			matches = append(matches, p.ID)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("group %q not found, specify the group's principal ID, e.g. openldap_group://cn=admins,ou=groups,dc=example,dc=org, or a group name that exists in an enabled auth provider", group)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%d groups match %q, specify one of the group principal IDs: %s", len(matches), group, strings.Join(matches, ", "))
	}
}
