package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type getPRTBsParams struct {
	ClusterID string `json:"cluster" jsonschema:"the ID of the cluster that the project belongs to"`
	ProjectID string `json:"project,omitempty" jsonschema:"the ID of the project to get permissions for. Optional — if omitted, permissions are returned across all projects in the cluster"`
	User      string `json:"user" jsonschema:"the user to get permissions for"`
}

type returnType struct {
	PRTB         *unstructured.Unstructured `json:"prtb" jsonschema:"the project role template binding"`
	RoleTemplate *unstructured.Unstructured `json:"roleTemplate" jsonschema:"the role template associated with the PRTB"`
	Rules        []*rbacv1.PolicyRule       `json:"rules" jsonschema:"the rules associated with the role template"`
}

var zapListProjectPermissions = zap.String("tool", "listProjectPermissions")

func (t *Tools) getPRTBs(ctx context.Context, toolReq *mcp.CallToolRequest) ([]*unstructured.Unstructured, error) {
	zap.L().Debug("getPRTBs called")
	prtbs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "projectroletemplatebinding",
		// Namespace omitted — lists across all namespaces
		URL:   t.rancherURL(toolReq),
		Token: middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get project role template bindings", zapListProjectPermissions, zap.Error(err))
		return nil, err
	}
	return prtbs, nil
}

func (t *Tools) getPRTBsForProject(ctx context.Context, toolReq *mcp.CallToolRequest, params getPRTBsParams) ([]*unstructured.Unstructured, error) {
	zap.L().Debug("getPRTBsForProject called")
	projectBackingNamespace := params.ClusterID + "-" + params.ProjectID

	prtbs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   "local",
		Kind:      "projectroletemplatebinding",
		Namespace: projectBackingNamespace,
		URL:       t.rancherURL(toolReq),
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get project role template bindings", zapListProjectPermissions, zap.Error(err))
		return nil, err
	}

	return prtbs, nil
}

func (t *Tools) resolveRoleTemplates(ctx context.Context, toolReq *mcp.CallToolRequest, prtbs []*unstructured.Unstructured) ([]returnType, error) {
	zap.L().Debug("resolveRoleTemplates called")

	var result []returnType
	for _, prtb := range prtbs {
		roleTemplateName, found, err := unstructured.NestedString(prtb.Object, "roleTemplateName")
		if err != nil || !found {
			zap.L().Error("failed to get role template name from PRTB", zapListProjectPermissions, zap.Error(err))
			return nil, err
		}

		roleTemplate, err := t.client.GetResource(ctx, client.GetParams{
			Cluster: "local",
			Kind:    "roletemplate",
			Name:    roleTemplateName,
			URL:     t.rancherURL(toolReq),
			Token:   middleware.Token(ctx),
		})
		if err != nil {
			zap.L().Error("failed to get role template", zapListProjectPermissions, zap.Error(err))
			return nil, err
		}
		rules, found, err := unstructured.NestedSlice(roleTemplate.Object, "rules")
		if err != nil || !found {
			zap.L().Error("failed to get rules from role template", zapListProjectPermissions, zap.Error(err))
			return nil, err
		}

		var policyRules []*rbacv1.PolicyRule
		for _, rule := range rules {
			ruleMap, ok := rule.(map[string]any)
			if !ok {
				continue
			}
			var policyRule rbacv1.PolicyRule
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ruleMap, &policyRule); err != nil {
				zap.L().Error("failed to convert rule to policy rule", zapListProjectPermissions, zap.Error(err))
				return nil, err
			}
			policyRules = append(policyRules, &policyRule)
		}

		result = append(result, returnType{PRTB: prtb, RoleTemplate: roleTemplate, Rules: policyRules})
	}
	return result, nil
}

func (t *Tools) listProjectPermissions(ctx context.Context, toolReq *mcp.CallToolRequest, params getPRTBsParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listProjectPermissions called", zap.String("cluster", params.ClusterID), zap.String("user", params.User))

	var err error

	// Get all PRTBs
	var prtbs []*unstructured.Unstructured
	if params.ProjectID == "" {
		zap.L().Debug("Fetching PRTBs for all projects in the cluster")
		prtbs, err = t.getPRTBs(ctx, toolReq)
	} else {
		zap.L().Debug("Fetching PRTBs for specific project", zap.String("project", params.ProjectID))
		prtbs, err = t.getPRTBsForProject(ctx, toolReq, params)
	}

	if err != nil {
		zap.L().Error("failed to get project role template bindings", zapListProjectPermissions, zap.Error(err))
		return nil, nil, err
	}

	// Filter the resources to only include those that match the specified user
	var filteredPRTBs []*unstructured.Unstructured
	for _, prtb := range prtbs {
		if user, found, err := unstructured.NestedString(prtb.Object, "userName"); err == nil && found && user == params.User {
			filteredPRTBs = append(filteredPRTBs, prtb)
		}
	}

	result, err := t.resolveRoleTemplates(ctx, toolReq, filteredPRTBs)
	if err != nil {
		zap.L().Error("failed to get role templates", zapListProjectPermissions, zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponseAny(result)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListProjectPermissions, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, result, nil
}
