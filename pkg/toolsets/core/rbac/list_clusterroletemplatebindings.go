package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

var zapListClusterRoleTemplateBindings = zap.String("tool", "listClusterRoleTemplateBindings")

type listCRTBParams struct {
	Cluster string `json:"cluster" jsonschema:"the name of the cluster the search is scoped to"`
	User    string `json:"user,omitempty" jsonschema:"(optional) the username or user ID to get permissions for. Bindings for the groups the user belongs to are included. Cannot be combined with group"`
	Group   string `json:"group,omitempty" jsonschema:"(optional) the group principal name to get permissions for, e.g. github_team://1234. Cannot be combined with user"`
}

// listClusterRoleTemplateBindings retrieves a cluster role template binding resource.
func (t *Tools) listClusterRoleTemplateBindings(ctx context.Context, toolReq *mcp.CallToolRequest, params listCRTBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listClusterRoleTemplateBindings called", zap.String("cluster", params.Cluster))

	if params.User != "" && params.Group != "" {
		return nil, nil, errUserAndGroup
	}

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		zap.L().Error("failed to resolve cluster ID", zapListClusterRoleTemplateBindings, zap.Error(err))
		return nil, nil, err
	}
	namespace := clusterID

	crtbs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   "local",
		Kind:      "clusterroletemplatebinding",
		Namespace: namespace,
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get cluster role template bindings", zapListClusterRoleTemplateBindings, zap.Error(err))
		return nil, nil, err
	}

	// Filter the resources to only include those for the specified user, or the groups the user belongs to
	var notes []string
	if params.User != "" {
		subjects, err := t.getUserSubjects(ctx, params.User)
		if err != nil {
			zap.L().Error("failed to resolve user", zapListClusterRoleTemplateBindings, zap.Error(err))
			return nil, nil, err
		}
		crtbs = filterBindingsForUser(crtbs, subjects)
		notes = userGroupsNote(subjects.groups)
	}
	// Filter the resources to only include those that match the specified group
	if params.Group != "" {
		crtbs = filterBindingsForGroup(crtbs, params.Group)
	}
	mcpResponse, err := response.CreateMcpResponse(crtbs, "local", notes...)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListClusterRoleTemplateBindings, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
