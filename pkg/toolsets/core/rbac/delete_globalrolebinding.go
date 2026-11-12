package rbac

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapDeleteGlobalRoleBinding = zap.String("tool", "deleteGlobalRoleBinding")

type deleteGRBParams struct {
	Name string `json:"name" jsonschema:"the name of the global role binding to delete"`
}

// deletedGlobalRoleBinding is the response of deleteGlobalRoleBinding. It only identifies the
// binding, as that is all that is needed to confirm what was removed.
type deletedGlobalRoleBinding struct {
	GlobalRoleBinding map[string]any `json:"globalRoleBinding"`
	Note              string         `json:"note"`
}

// deleteGlobalRoleBinding removes a global role from a user or group by deleting the global role binding.
func (t *Tools) deleteGlobalRoleBinding(ctx context.Context, toolReq *mcp.CallToolRequest, params deleteGRBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("deleteGlobalRoleBinding called", zap.String("name", params.Name))

	// Fetch the binding first so the response shows what was removed.
	grb, err := t.getGlobalRoleBindingObj(ctx, params.Name)
	if err != nil {
		zap.L().Error("failed to get global role binding", zapDeleteGlobalRoleBinding, zap.Error(err))
		return nil, nil, err
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), "", "local", converter.K8sKindsToGVRs["globalrolebinding"])
	if err != nil {
		return nil, nil, err
	}

	if err := resourceInterface.Delete(ctx, params.Name, metav1.DeleteOptions{}); err != nil {
		zap.L().Error("failed to delete global role binding", zapDeleteGlobalRoleBinding, zap.Error(err))
		return nil, nil, fmt.Errorf("failed to delete global role binding: %w", err)
	}

	// The response has no UI context because the binding no longer exists and cannot be linked to.
	mcpResponse, err := response.CreateMcpResponseAny(deletedGlobalRoleBinding{
		GlobalRoleBinding: summarizeGlobalRoleBinding(grb, true),
		Note:              "the global role binding has been deleted",
	})
	if err != nil {
		zap.L().Error("failed to create mcp response", zapDeleteGlobalRoleBinding, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// deleteGlobalRoleBindingPlan plans the deletion of a global role binding.
// It returns the JSON representation of the global role binding to be deleted without actually deleting it.
func (t *Tools) deleteGlobalRoleBindingPlan(ctx context.Context, _ *mcp.CallToolRequest, params deleteGRBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("deleteGlobalRoleBindingPlan called", zap.String("name", params.Name))

	grb, err := t.getGlobalRoleBindingObj(ctx, params.Name)
	if err != nil {
		zap.L().Error("failed to get global role binding", zap.String("tool", "deleteGlobalRoleBindingPlan"), zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{response.NewDeleteResourceInput(grb, "local")})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "deleteGlobalRoleBindingPlan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// getGlobalRoleBindingObj retrieves a global role binding to be deleted, with server-managed fields
// and its status removed, so plans only show what is relevant to the binding.
func (t *Tools) getGlobalRoleBindingObj(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	grb, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: "local",
		Kind:    "globalrolebinding",
		Name:    name,
		Token:   middleware.Token(ctx),
	})
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("global role binding %q not found, use listGlobalRoleBindings to find the name of the binding: %w", name, err)
	}

	if err != nil {
		return nil, err
	}

	response.RemoveServerManagedFields(grb)
	unstructured.RemoveNestedField(grb.Object, "status")

	return grb, nil
}
