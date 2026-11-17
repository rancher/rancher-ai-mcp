package rbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapCreateGlobalRoleBinding = zap.String("tool", "createGlobalRoleBinding")

type createGRBParams struct {
	GlobalRole string `json:"globalRole" jsonschema:"the name of the global role to assign"`
	User       string `json:"user,omitempty" jsonschema:"the username or user ID, or the display name or principal ID, of the user to assign the global role to. Exactly one of user or group must be set"`
	Group      string `json:"group,omitempty" jsonschema:"the group to assign the global role to: its principal ID (e.g. github_team://123 or openldap_group://cn=devs,ou=groups,dc=example,dc=org) or its name, which is looked up in the enabled auth providers. Exactly one of user or group must be set"`
}

// createGlobalRoleBinding assigns a global role to a user or group by creating a global role binding.
func (t *Tools) createGlobalRoleBinding(ctx context.Context, toolReq *mcp.CallToolRequest, params createGRBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createGlobalRoleBinding called", zap.String("globalRole", params.GlobalRole))

	grb, existing, err := t.resolveGlobalRoleBindingObj(ctx, params)
	if err != nil {
		zap.L().Error("failed to create global role binding object", zapCreateGlobalRoleBinding, zap.Error(err))
		return nil, nil, err
	}

	if existing != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: existingGlobalRoleBindingMessage(existing)}},
		}, nil, nil
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

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, "local", response.WithSummarizedStatus())
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

	grb, existing, err := t.resolveGlobalRoleBindingObj(ctx, params)
	if err != nil {
		zap.L().Error("failed to create global role binding object", zap.String("tool", "createGlobalRoleBindingPlan"), zap.Error(err))
		return nil, nil, err
	}

	if existing != nil {
		mcpResponse, err := response.CreateNoChangesPlanResponse(existingGlobalRoleBindingMessage(existing))
		if err != nil {
			zap.L().Error("failed to create no changes plan response", zap.String("tool", "createGlobalRoleBindingPlan"), zap.Error(err))
			return nil, nil, err
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
		}, nil, nil
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
// If a global role binding already grants the role to the same subject, it is returned as existing instead.
func (t *Tools) resolveGlobalRoleBindingObj(ctx context.Context, params createGRBParams) (grb, existing *unstructured.Unstructured, err error) {
	if err := validateGlobalRoleBindingParams(params); err != nil {
		return nil, nil, err
	}

	if params.User != "" {
		userID, err := t.resolveUserID(ctx, params.User)
		if err != nil {
			return nil, nil, err
		}

		params.User = userID
	} else {
		groupPrincipal, err := t.resolveGroupPrincipal(ctx, params.Group)
		if err != nil {
			return nil, err
		}

		params.Group = groupPrincipal
	}

	grb = createGlobalRoleBindingObj(params)

	existing, err = t.findExistingGlobalRoleBinding(ctx, grb)
	if err != nil {
		return nil, nil, err
	}

	return grb, existing, nil
}

// findExistingGlobalRoleBinding returns an active global role binding that grants the same role
// to the same user or group as grb, or nil if there is none.
func (t *Tools) findExistingGlobalRoleBinding(ctx context.Context, grb *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	grbs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "globalrolebinding",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get global role bindings: %w", err)
	}

	subjectField := "userName"
	if _, ok := grb.Object[subjectField]; !ok {
		subjectField = "groupPrincipalName"
	}

	roleName, _, _ := unstructured.NestedString(grb.Object, "globalRoleName")
	subject, _, _ := unstructured.NestedString(grb.Object, subjectField)

	grbs = filterResourcesByField(grbs, "globalRoleName", roleName)
	for _, existing := range filterResourcesByField(grbs, subjectField, subject) {
		// GRBs are kept until Rancher's finalizers have run, ignore those that are already being deleted.
		if existing.GetDeletionTimestamp() == nil {
			return existing, nil
		}
	}

	return nil, nil
}

func existingGlobalRoleBindingMessage(grb *unstructured.Unstructured) string {
	roleName, _, _ := unstructured.NestedString(grb.Object, "globalRoleName")
	return fmt.Sprintf("global role '%s' is already granted by global role binding '%s', no changes needed", roleName, grb.GetName())
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
