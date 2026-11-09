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

var zapDeleteGlobalRole = zap.String("tool", "deleteGlobalRole")

type deleteGlobalRoleParams struct {
	Name string `json:"name" jsonschema:"the name of the global role to delete"`
}

// deletedGlobalRole is the response of deleteGlobalRole. The global role bindings that Rancher
// deleted with the global role are summarized, as there can be many of them.
type deletedGlobalRole struct {
	GlobalRole         map[string]any   `json:"globalRole"`
	GlobalRoleBindings []map[string]any `json:"globalRoleBindings,omitempty"`
	Note               string           `json:"note"`
}

// deleteGlobalRole deletes a global role. Rancher also deletes the global role bindings
// for the global role, so they are included in the response.
func (t *Tools) deleteGlobalRole(ctx context.Context, toolReq *mcp.CallToolRequest, params deleteGlobalRoleParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("deleteGlobalRole called", zap.String("name", params.Name))

	globalRole, grbs, err := t.getGlobalRoleForDeletion(ctx, params.Name)
	if err != nil {
		zap.L().Error("failed to get global role for deletion", zapDeleteGlobalRole, zap.Error(err))
		return nil, nil, err
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), "", "local", converter.K8sKindsToGVRs["globalrole"])
	if err != nil {
		return nil, nil, err
	}

	if err := resourceInterface.Delete(ctx, params.Name, metav1.DeleteOptions{}); err != nil {
		zap.L().Error("failed to delete global role", zapDeleteGlobalRole, zap.Error(err))
		return nil, nil, fmt.Errorf("failed to delete global role: %w", err)
	}

	deleted := deletedGlobalRole{
		GlobalRole: globalRole.Object,
		Note:       "the global role has been deleted",
	}
	for _, grb := range grbs {
		deleted.GlobalRoleBindings = append(deleted.GlobalRoleBindings, summarizeGlobalRoleBinding(grb, false))
	}

	switch len(grbs) {
	case 0:
	case 1:
		deleted.Note = "the global role has been deleted, along with the global role binding that assigned it to a user or group"
	default:
		deleted.Note = fmt.Sprintf("the global role has been deleted, along with %d global role bindings that assigned it to users or groups", len(grbs))
	}

	// The response has no UI context because the resources no longer exist and cannot be linked to.
	mcpResponse, err := response.CreateMcpResponseAny(deleted)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapDeleteGlobalRole, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// deleteGlobalRolePlan plans the deletion of a global role.
// It returns the JSON representation of the global role, and the global role bindings that
// Rancher deletes with it, without actually deleting them.
func (t *Tools) deleteGlobalRolePlan(ctx context.Context, _ *mcp.CallToolRequest, params deleteGlobalRoleParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("deleteGlobalRolePlan called", zap.String("name", params.Name))

	globalRole, grbs, err := t.getGlobalRoleForDeletion(ctx, params.Name)
	if err != nil {
		zap.L().Error("failed to get global role for deletion", zap.String("tool", "deleteGlobalRolePlan"), zap.Error(err))
		return nil, nil, err
	}

	planResources := []response.PlanResource{response.NewDeleteResourceInput(globalRole, "local")}
	for _, grb := range grbs {
		planResources = append(planResources, response.NewDeleteResourceInput(grb, "local"))
	}

	mcpResponse, err := response.CreatePlanResponse(planResources)
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "deleteGlobalRolePlan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// getGlobalRoleForDeletion retrieves a global role and the global role bindings that assign it,
// with server-managed fields and their status removed. Built-in global roles cannot be deleted.
func (t *Tools) getGlobalRoleForDeletion(ctx context.Context, name string) (*unstructured.Unstructured, []*unstructured.Unstructured, error) {
	globalRole, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: "local",
		Kind:    "globalrole",
		Name:    name,
		Token:   middleware.Token(ctx),
	})
	if apierrors.IsNotFound(err) {
		return nil, nil, fmt.Errorf("global role %q not found, use getGlobalRole to find the name from a display name: %w", name, err)
	}

	if err != nil {
		return nil, nil, err
	}

	if builtin, _, _ := unstructured.NestedBool(globalRole.Object, "builtin"); builtin {
		return nil, nil, fmt.Errorf("global role %q is built-in and cannot be deleted", name)
	}

	removeServerManagedFields(globalRole)
	unstructured.RemoveNestedField(globalRole.Object, "status")

	grbs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "globalrolebinding",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		return nil, nil, err
	}

	grbs = filterResourcesByField(grbs, "globalRoleName", name)
	for _, grb := range grbs {
		removeServerManagedFields(grb)
		unstructured.RemoveNestedField(grb.Object, "status")
	}

	return globalRole, grbs, nil
}
