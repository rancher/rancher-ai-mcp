package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

var zapListGlobalRoleBindings = zap.String("tool", "listGlobalRoleBindings")

type listGRBParams struct {
	User       string `json:"user,omitempty" jsonschema:"(optional) the username or user ID to get permissions for. Bindings for the groups the user belongs to are included. Cannot be combined with group"`
	Group      string `json:"group,omitempty" jsonschema:"(optional) the group principal name to get permissions for, e.g. github_team://1234. Cannot be combined with user"`
	GlobalRole string `json:"globalRole,omitempty" jsonschema:"(optional) the name of the global role to list bindings for"`
}

// listGlobalRoleBindings lists global role binding resources.
func (t *Tools) listGlobalRoleBindings(ctx context.Context, toolReq *mcp.CallToolRequest, params listGRBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listGlobalRoleBindings called")

	if params.User != "" && params.Group != "" {
		return nil, nil, errUserAndGroup
	}

	grbs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "globalrolebinding",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list global role bindings", zapListGlobalRoleBindings, zap.Error(err))
		return nil, nil, err
	}

	// Filter the resources to only include those for the specified user, or the groups the user belongs to
	var notes []string
	if params.User != "" {
		subjects, err := t.getUserSubjects(ctx, params.User)
		if err != nil {
			zap.L().Error("failed to resolve user", zapListGlobalRoleBindings, zap.Error(err))
			return nil, nil, err
		}
		grbs = filterBindingsForUser(grbs, subjects)
		notes = userGroupsNote(subjects.groups)
	}
	// Filter the resources to only include those that match the specified group
	if params.Group != "" {
		grbs = filterBindingsForGroup(grbs, params.Group)
	}
	// Filter the resources to only include those that match the specified global role
	if params.GlobalRole != "" {
		grbs = filterResourcesByField(grbs, "globalRoleName", params.GlobalRole)
	}
	mcpResponse, err := response.CreateMcpResponse(grbs, "local", notes...)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListGlobalRoleBindings, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
