package rbac

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapDeleteGlobalRole = zap.String("tool", "deleteGlobalRole")

type deleteGlobalRoleParams struct {
	Name string `json:"name" jsonschema:"the name of the global role to delete"`
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

	note := "the global role has been deleted"
	if len(grbs) > 0 {
		note = fmt.Sprintf("the global role has been deleted, along with %d global role binding(s) that assigned it to users or groups", len(grbs))
	}
	mcpResponse, err := createDeletedResourcesResponse(append([]*unstructured.Unstructured{globalRole}, grbs...), note)
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
// with server-managed fields removed. Built-in global roles cannot be deleted.
func (t *Tools) getGlobalRoleForDeletion(ctx context.Context, name string) (*unstructured.Unstructured, []*unstructured.Unstructured, error) {
	globalRole, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: "local",
		Kind:    "globalrole",
		Name:    name,
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		return nil, nil, err
	}
	if builtin, _, _ := unstructured.NestedBool(globalRole.Object, "builtin"); builtin {
		return nil, nil, fmt.Errorf("global role %q is built-in and cannot be deleted", name)
	}
	removeServerManagedFields(globalRole)

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
	}

	return globalRole, grbs, nil
}
