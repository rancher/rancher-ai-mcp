package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapListClusterRoleTemplateBindings = zap.String("tool", "listClusterRoleTemplateBindings")

type listCRTBParams struct {
	Cluster string `json:"cluster" jsonschema:"the name of the cluster the search is scoped to"`
	User    string `json:"user,omitempty" jsonschema:"(optional) the user to get permissions for"`
}

func filterCRTBsByUser(crtbs []*unstructured.Unstructured, user string) []*unstructured.Unstructured {
	var filteredCRTBs []*unstructured.Unstructured
	for _, crtb := range crtbs {
		if userName, found, err := unstructured.NestedString(crtb.Object, "userName"); err == nil && found && userName == user {
			filteredCRTBs = append(filteredCRTBs, crtb)
		}
	}
	return filteredCRTBs
}

// listClusterRoleTemplateBindings retrieves a cluster role template binding resource.
func (t *Tools) listClusterRoleTemplateBindings(ctx context.Context, toolReq *mcp.CallToolRequest, params listCRTBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listClusterRoleTemplateBindings called", zap.String("cluster", params.Cluster), zap.String("user", params.User))

	namespace := ""
	if params.Cluster != "" {
		clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
		if err != nil {
			zap.L().Error("failed to resolve cluster ID", zapListClusterRoleTemplateBindings, zap.Error(err))
			return nil, nil, err
		}
		namespace = clusterID
	}

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

	// Filter the resources to only include those that match the specified user
	if params.User != "" {
		crtbs = filterCRTBsByUser(crtbs, params.User)
	}

	mcpResponse, err := response.CreateMcpResponse(crtbs, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListClusterRoleTemplateBindings, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
