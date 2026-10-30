package rbac

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	zapDeleteProjectRoleTemplateBinding     = zap.String("tool", "deleteProjectRoleTemplateBinding")
	zapDeleteProjectRoleTemplateBindingPlan = zap.String("tool", "deleteProjectRoleTemplateBindingPlan")
)

type deletePRTBParams struct {
	Cluster      string `json:"cluster" jsonschema:"the name or ID of the cluster the project belongs to"`
	Project      string `json:"project" jsonschema:"the ID (e.g. p-abc) or display name of the project"`
	User         string `json:"user,omitempty" jsonschema:"the username or user ID to remove access from. Exactly one of user or group must be specified"`
	Group        string `json:"group,omitempty" jsonschema:"the group principal ID to remove access from (e.g. github_team://123). Exactly one of user or group must be specified"`
	RoleTemplate string `json:"roleTemplate,omitempty" jsonschema:"the name of the project role template to remove (e.g. project-owner, project-member, read-only). Defaults to project-member"`
}

// deleteProjectRoleTemplateBinding removes a role from a user or group in a project.
func (t *Tools) deleteProjectRoleTemplateBinding(ctx context.Context, toolReq *mcp.CallToolRequest, params deletePRTBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("deleteProjectRoleTemplateBinding called", zap.String("cluster", params.Cluster), zap.String("project", params.Project))

	target, prtbs, err := t.findPRTBsToDelete(ctx, params)
	if err != nil {
		zap.L().Error("failed to find project role template bindings", zapDeleteProjectRoleTemplateBinding, zap.Error(err))
		return nil, nil, err
	}
	if len(prtbs) == 0 {
		return noBindingResult(target), nil, nil
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), target.namespace, "local", converter.K8sKindsToGVRs["projectroletemplatebinding"])
	if err != nil {
		return nil, nil, err
	}

	var deleted []string
	for _, prtb := range prtbs {
		if err := resourceInterface.Delete(ctx, prtb.GetName(), metav1.DeleteOptions{}); err != nil {
			zap.L().Error("failed to delete project role template binding", zapDeleteProjectRoleTemplateBinding, zap.Error(err))
			return nil, nil, fmt.Errorf("failed to delete project role template binding '%s/%s': %w", prtb.GetNamespace(), prtb.GetName(), err)
		}
		deleted = append(deleted, prtb.GetNamespace()+"/"+prtb.GetName())
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("role '%s' removed in project '%s' by deleting project role template binding(s) %s",
				target.roleTemplateName, target.projectName, strings.Join(deleted, ", ")),
		}},
	}, nil, nil
}

// deleteProjectRoleTemplateBindingPlan plans removing a role from a user or group in a project.
// It returns the PRTBs to be deleted without actually deleting them.
func (t *Tools) deleteProjectRoleTemplateBindingPlan(ctx context.Context, toolReq *mcp.CallToolRequest, params deletePRTBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("deleteProjectRoleTemplateBindingPlan called", zap.String("cluster", params.Cluster), zap.String("project", params.Project))

	target, prtbs, err := t.findPRTBsToDelete(ctx, params)
	if err != nil {
		zap.L().Error("failed to find project role template bindings", zapDeleteProjectRoleTemplateBindingPlan, zap.Error(err))
		return nil, nil, err
	}
	if len(prtbs) == 0 {
		return noBindingResult(target), nil, nil
	}

	var deleteResources []response.PlanResource
	for _, prtb := range prtbs {
		deleteResources = append(deleteResources, response.NewDeleteResourceInput(prtb, "local"))
	}
	mcpResponse, err := response.CreatePlanResponse(deleteResources)
	if err != nil {
		zap.L().Error("failed to create plan response", zapDeleteProjectRoleTemplateBindingPlan, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// findPRTBsToDelete returns the PRTBs granting the role to the subject in the project.
// The role template is not validated so that bindings can be removed even if the role template has since been locked.
func (t *Tools) findPRTBsToDelete(ctx context.Context, params deletePRTBParams) (*prtbTarget, []*unstructured.Unstructured, error) {
	target, err := t.resolvePRTBTarget(ctx, createPRTBParams(params))
	if err != nil {
		return nil, nil, err
	}

	prtbs, err := t.findPRTBs(ctx, target)
	if err != nil {
		return nil, nil, err
	}

	return target, prtbs, nil
}

func noBindingResult(target *prtbTarget) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("role '%s' is not granted in project '%s', no changes needed", target.roleTemplateName, target.projectName),
		}},
	}
}
