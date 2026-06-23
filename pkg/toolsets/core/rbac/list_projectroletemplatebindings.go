package rbac

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapListProjectRoleTemplateBindings = zap.String("tool", "listProjectRoleTemplateBindings")

type listPRTBParams struct {
	Cluster   string `json:"cluster" jsonschema:"the name or ID of the cluster resource the project belongs to"`
	ProjectID string `json:"projectID,omitempty" jsonschema:"(optional) the ID of the project resource (e.g. p-abc)"`
	User      string `json:"user,omitempty" jsonschema:"(optional) the user to get permissions for"`
}

func filterPRTBsByUser(prtbs []*unstructured.Unstructured, user string) []*unstructured.Unstructured {
	var filteredPRTBs []*unstructured.Unstructured
	for _, prtb := range prtbs {
		if userName, found, err := unstructured.NestedString(prtb.Object, "userName"); err == nil && found && userName == user {
			filteredPRTBs = append(filteredPRTBs, prtb)
		}
	}
	return filteredPRTBs
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
	zap.L().Debug("listProjectRoleTemplateBindings called", zap.String("cluster", params.Cluster), zap.String("projectID", params.ProjectID), zap.String("user", params.User))

	clusterID := params.Cluster
	if params.Cluster != "" {
		var err error
		clusterID, err = t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
		if err != nil {
			zap.L().Error("failed to resolve cluster ID", zapListProjectRoleTemplateBindings, zap.Error(err))
			return nil, nil, err
		}
	}

	namespace := ""
	if params.ProjectID != "" {
		projectBackingNamespace := clusterID + "-" + params.ProjectID
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

	// Filter the resources to only include those that match the specified user
	if params.User != "" {
		prtbs = filterPRTBsByUser(prtbs, params.User)
	}

	// Filter the resources to only include those that match the specified cluster
	if clusterID != "" {
		prtbs = filterPRTBsByCluster(prtbs, clusterID)
	}

	mcpResponse, err := response.CreateMcpResponse(prtbs, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListProjectRoleTemplateBindings, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
