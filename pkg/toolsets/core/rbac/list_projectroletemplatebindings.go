package rbac

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/toolsets/core/projects"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapListProjectRoleTemplateBindings = zap.String("tool", "listProjectRoleTemplateBindings")

type listPRTBParams struct {
	Cluster   string `json:"cluster" jsonschema:"the name or ID of the cluster resource the project belongs to"`
	ProjectID string `json:"projectID,omitempty" jsonschema:"(optional) the ID of the project resource (e.g. p-abc)"`
	User      string `json:"user,omitempty" jsonschema:"(optional) the username or user ID to get permissions for. Bindings for the groups the user belongs to are included. Cannot be combined with group"`
	Group     string `json:"group,omitempty" jsonschema:"(optional) the group principal name to get permissions for, e.g. github_team://1234. Cannot be combined with user"`
}

func filterPRTBsByCluster(prtbs []*unstructured.Unstructured, clusterName string) []*unstructured.Unstructured {
	var filteredPRTBs []*unstructured.Unstructured
	for _, prtb := range prtbs {
		projectNameField, found, err := unstructured.NestedString(prtb.Object, "projectName")
		if err != nil || !found {
			zap.L().Error("failed to get projectName from PRTB", zapListProjectRoleTemplateBindings, zap.Error(err))
		}
		cluster, _, found := strings.Cut(projectNameField, ":")
		if !found || cluster != clusterName {
			continue
		}
		filteredPRTBs = append(filteredPRTBs, prtb)
	}
	return filteredPRTBs
}

// listProjectRoleTemplateBindings retrieves a project role template binding resource.
func (t *Tools) listProjectRoleTemplateBindings(ctx context.Context, toolReq *mcp.CallToolRequest, params listPRTBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listProjectRoleTemplateBindings called", zap.String("cluster", params.Cluster), zap.String("projectID", params.ProjectID))

	if params.User != "" && params.Group != "" {
		return nil, nil, errUserAndGroup
	}

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		zap.L().Error("failed to resolve cluster ID", zapListProjectRoleTemplateBindings, zap.Error(err))
		return nil, nil, err
	}

	namespace := ""
	if params.ProjectID != "" {
		_, projectResource, err := projects.GetProjectID(ctx, t.client, middleware.Token(ctx), clusterID, params.ProjectID)
		if err != nil {
			zap.L().Error("failed to get project by ID", zapListProjectRoleTemplateBindings, zap.Error(err))
			return nil, nil, err
		}
		projectBackingNamespace, err := projects.GetProjectBackingNamespace(projectResource)
		if err != nil {
			zap.L().Error("failed to get project backing namespace", zapListProjectRoleTemplateBindings, zap.Error(err))
			return nil, nil, err
		}
		namespace = projectBackingNamespace
	}

	prtbs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   "local",
		Kind:      "projectroletemplatebinding",
		Namespace: namespace,
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get project role template bindings", zapListProjectRoleTemplateBindings, zap.Error(err))
		return nil, nil, err
	}

	// Filter the resources to only include those for the specified user, or the groups the user belongs to
	var notes []string
	if params.User != "" {
		subjects, err := t.getUserSubjects(ctx, params.User)
		if err != nil {
			zap.L().Error("failed to resolve user", zapListProjectRoleTemplateBindings, zap.Error(err))
			return nil, nil, err
		}
		prtbs = filterBindingsForUser(prtbs, subjects)
		notes = userGroupsNote(subjects.groups)
	}
	// Filter the resources to only include those that match the specified group
	if params.Group != "" {
		prtbs = filterBindingsForGroup(prtbs, params.Group)
	}

	// Filter the resources to only include those that match the specified cluster
	if clusterID != "" {
		prtbs = filterPRTBsByCluster(prtbs, clusterID)
	}

	mcpResponse, err := response.CreateMcpResponse(prtbs, "local", notes...)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListProjectRoleTemplateBindings, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
