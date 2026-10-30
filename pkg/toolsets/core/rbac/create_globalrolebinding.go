package rbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapCreateGlobalRoleBinding = zap.String("tool", "createGlobalRoleBinding")

type createGRBParams struct {
	GlobalRole string `json:"globalRole" jsonschema:"the name of the global role to assign"`
	User       string `json:"user,omitempty" jsonschema:"the username or user ID to assign the global role to. Exactly one of user or group must be set"`
	Group      string `json:"group,omitempty" jsonschema:"the group principal name to assign the global role to. Exactly one of user or group must be set"`
}

// createGlobalRoleBinding assigns a global role to a user or group by creating a global role binding.
func (t *Tools) createGlobalRoleBinding(ctx context.Context, toolReq *mcp.CallToolRequest, params createGRBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createGlobalRoleBinding called", zap.String("globalRole", params.GlobalRole))

	grb, err := t.resolveGlobalRoleBindingObj(ctx, params)
	if err != nil {
		zap.L().Error("failed to create global role binding object", zapCreateGlobalRoleBinding, zap.Error(err))
		return nil, nil, err
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), "", "local", converter.K8sKindsToGVRs["globalrolebinding"])
	if err != nil {
		return nil, nil, err
	}

	obj, err := resourceInterface.Create(ctx, grb, metav1.CreateOptions{})
	if err != nil {
		zap.L().Error("failed to create global role binding", zapCreateGlobalRoleBinding, zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create global role binding: %w", err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapCreateGlobalRoleBinding, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// createGlobalRoleBindingPlan plans the creation of a global role binding.
// It returns the JSON representation of the global role binding to be created without actually creating it.
func (t *Tools) createGlobalRoleBindingPlan(ctx context.Context, _ *mcp.CallToolRequest, params createGRBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createGlobalRoleBindingPlan called", zap.String("globalRole", params.GlobalRole))

	grb, err := t.resolveGlobalRoleBindingObj(ctx, params)
	if err != nil {
		zap.L().Error("failed to create global role binding object", zap.String("tool", "createGlobalRoleBindingPlan"), zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{response.NewCreateResourceInput(grb, "local")})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "createGlobalRoleBindingPlan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// resolveGlobalRoleBindingObj validates the parameters, resolves a username to a user ID,
// and returns the global role binding to be created.
func (t *Tools) resolveGlobalRoleBindingObj(ctx context.Context, params createGRBParams) (*unstructured.Unstructured, error) {
	if err := validateGlobalRoleBindingParams(params); err != nil {
		return nil, err
	}
	if params.User != "" {
		userID, err := t.resolveUserID(ctx, params.User)
		if err != nil {
			return nil, err
		}
		params.User = userID
	}

	return createGlobalRoleBindingObj(params), nil
}

func validateGlobalRoleBindingParams(params createGRBParams) error {
	if params.GlobalRole == "" {
		return errors.New("globalRole must be specified")
	}
	if (params.User == "") == (params.Group == "") {
		return errors.New("exactly one of user or group must be specified")
	}

	return nil
}

func createGlobalRoleBindingObj(params createGRBParams) *unstructured.Unstructured {
	grb := &unstructured.Unstructured{
		Object: map[string]any{
			"globalRoleName": params.GlobalRole,
		},
	}
	grb.SetKind("GlobalRoleBinding")
	grb.SetAPIVersion(converter.ManagementGroup + "/v3")
	grb.SetGenerateName("grb-")
	if params.User != "" {
		grb.Object["userName"] = params.User
	} else {
		grb.Object["groupPrincipalName"] = params.Group
	}

	return grb
}
