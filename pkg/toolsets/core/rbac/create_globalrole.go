package rbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapCreateGlobalRole = zap.String("tool", "createGlobalRole")

type policyRule struct {
	APIGroups     []string `json:"apiGroups,omitempty" jsonschema:"the API groups the rule applies to. Use an empty string for the core API group. Defaults to the core API group if not specified"`
	Resources     []string `json:"resources" jsonschema:"the resources the rule applies to, e.g. configmaps or deployments"`
	Verbs         []string `json:"verbs" jsonschema:"the verbs allowed on the resources, e.g. get, list, watch, create, update, patch, delete. Use * for all verbs"`
	ResourceNames []string `json:"resourceNames,omitempty" jsonschema:"(optional) restricts the rule to resources with these names"`
}

type createGlobalRoleParams struct {
	Name                  string       `json:"name,omitempty" jsonschema:"(optional) the name of the global role resource. A name is generated if not specified"`
	DisplayName           string       `json:"displayName" jsonschema:"the display name of the global role"`
	Description           string       `json:"description,omitempty" jsonschema:"(optional) a description of the global role"`
	Rules                 []policyRule `json:"rules,omitempty" jsonschema:"the permissions granted by the global role in the local cluster. At least one rule or inherited cluster role must be specified"`
	InheritedClusterRoles []string     `json:"inheritedClusterRoles,omitempty" jsonschema:"(optional) names of cluster role templates granted in every downstream cluster"`
}

// createGlobalRoleInputSchema builds the input schema for the create global role tools.
func createGlobalRoleInputSchema() *jsonschema.Schema {
	s, err := jsonschema.For[createGlobalRoleParams](nil)
	if err != nil {
		panic(fmt.Errorf("failed to build create global role input schema: %w", err))
	}
	utils.ForceArrayTypes(s)

	return s
}

// createGlobalRole creates a global role resource.
func (t *Tools) createGlobalRole(ctx context.Context, toolReq *mcp.CallToolRequest, params createGlobalRoleParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createGlobalRole called", zap.String("displayName", params.DisplayName))

	globalRole, err := createGlobalRoleObj(params)
	if err != nil {
		zap.L().Error("failed to create global role object", zapCreateGlobalRole, zap.Error(err))
		return nil, nil, err
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), "", "local", converter.K8sKindsToGVRs["globalrole"])
	if err != nil {
		return nil, nil, err
	}

	obj, err := resourceInterface.Create(ctx, globalRole, metav1.CreateOptions{})
	if err != nil {
		zap.L().Error("failed to create global role", zapCreateGlobalRole, zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create global role: %w", err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapCreateGlobalRole, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// createGlobalRolePlan plans the creation of a global role.
// It returns the JSON representation of the global role to be created without actually creating it.
func (t *Tools) createGlobalRolePlan(_ context.Context, _ *mcp.CallToolRequest, params createGlobalRoleParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createGlobalRolePlan called", zap.String("displayName", params.DisplayName))

	globalRole, err := createGlobalRoleObj(params)
	if err != nil {
		zap.L().Error("failed to create global role object", zap.String("tool", "createGlobalRolePlan"), zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{response.NewCreateResourceInput(globalRole, "local")})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "createGlobalRolePlan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

func createGlobalRoleObj(params createGlobalRoleParams) (*unstructured.Unstructured, error) {
	if params.DisplayName == "" {
		return nil, errors.New("displayName must be specified")
	}
	if len(params.Rules) == 0 && len(params.InheritedClusterRoles) == 0 {
		return nil, errors.New("at least one rule or inherited cluster role must be specified")
	}

	globalRole := &unstructured.Unstructured{
		Object: map[string]any{
			"displayName": params.DisplayName,
		},
	}
	globalRole.SetKind("GlobalRole")
	globalRole.SetAPIVersion(converter.ManagementGroup + "/v3")
	if params.Name != "" {
		globalRole.SetName(params.Name)
	} else {
		globalRole.SetGenerateName("gr-")
	}
	if params.Description != "" {
		globalRole.Object["description"] = params.Description
	}

	if len(params.Rules) > 0 {
		rules := make([]any, 0, len(params.Rules))
		for i, r := range params.Rules {
			if len(r.Resources) == 0 || len(r.Verbs) == 0 {
				return nil, fmt.Errorf("rule %d must specify at least one resource and one verb", i)
			}
			apiGroups := r.APIGroups
			if len(apiGroups) == 0 {
				apiGroups = []string{""}
			}
			rule := map[string]any{
				"apiGroups": toAnySlice(apiGroups),
				"resources": toAnySlice(r.Resources),
				"verbs":     toAnySlice(r.Verbs),
			}
			if len(r.ResourceNames) > 0 {
				rule["resourceNames"] = toAnySlice(r.ResourceNames)
			}
			rules = append(rules, rule)
		}
		globalRole.Object["rules"] = rules
	}
	if len(params.InheritedClusterRoles) > 0 {
		globalRole.Object["inheritedClusterRoles"] = toAnySlice(params.InheritedClusterRoles)
	}

	return globalRole, nil
}

func toAnySlice(values []string) []any {
	result := make([]any, len(values))
	for i, v := range values {
		result[i] = v
	}
	return result
}
