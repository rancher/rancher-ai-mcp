package projects

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapGetProject = zap.String("tool", "getProject")

type getProjectParams struct {
	Name    string `json:"name" jsonschema:"the name of the project resource"`
	Cluster string `json:"cluster" jsonschema:"the name of the cluster resource the project belongs to"`
}

// getProject retrieves a project resource.
func (t *Tools) getProject(ctx context.Context, toolReq *mcp.CallToolRequest, params getProjectParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getProject called")

	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		zap.L().Error("failed to get cluster ID", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectID, projectResource, err := GetProjectID(ctx, t.client, middleware.Token(ctx), clusterID, params.Name)
	if err != nil {
		zap.L().Error("failed to get project", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectLabel, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{
			"field.cattle.io/projectId": projectID,
		},
	})
	if err != nil {
		zap.L().Error("failed to create label selector", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectNamespaces, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       clusterID,
		Kind:          "namespace",
		LabelSelector: projectLabel.String(),
		Token:         middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get namespaces for project", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	projectMembers, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   LocalCluster,
		Kind:      "projectroletemplatebinding",
		Namespace: projectID,
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get members for project", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	resources := append([]*unstructured.Unstructured{projectResource}, projectNamespaces...)
	for _, prtb := range projectMembers {
		slim := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": prtb.GetAPIVersion(),
				"kind":       prtb.GetKind(),
				"metadata": map[string]any{
					"name":      prtb.GetName(),
					"namespace": prtb.GetNamespace(),
				},
			},
		}
		// Return only the subject identity and role instead of the full PRTB,
		// which bloats the response for projects with many members.
		// A PRTB is either a user binding (userName/userPrincipalName) or a group binding
		// (groupName/groupPrincipalName), never both; copying only non-empty fields
		// naturally produces the right pair without branching.
		for _, field := range []string{"userName", "userPrincipalName", "groupName", "groupPrincipalName", "roleTemplateName"} {
			if v, _, _ := unstructured.NestedString(prtb.Object, field); v != "" {
				slim.Object[field] = v
			}
		}
		resources = append(resources, slim)
	}

	mcpResponse, err := response.CreateMcpResponse(resources, clusterID)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetProject, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
