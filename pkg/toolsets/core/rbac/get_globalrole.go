package rbac

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapGetGlobalRole = zap.String("tool", "getGlobalRole")

type getGlobalRoleParams struct {
	Name string `json:"name" jsonschema:"the name or display name of the global role to retrieve"`
}

// globalRoleSummary identifies a global role without its rules, so that
// the available roles can be listed when no role matches.
type globalRoleSummary struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

type noGlobalRoleMatch struct {
	Note                 string              `json:"note"`
	AvailableGlobalRoles []globalRoleSummary `json:"availableGlobalRoles"`
}

// getGlobalRole retrieves a global role resource by name or display name.
func (t *Tools) getGlobalRole(ctx context.Context, toolReq *mcp.CallToolRequest, params getGlobalRoleParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getGlobalRole called", zap.String("name", params.Name))

	globalRole, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: "local",
		Kind:    "globalrole",
		Name:    params.Name,
		Token:   middleware.Token(ctx),
	})
	if err == nil {
		return createGlobalRolesResponse([]*unstructured.Unstructured{globalRole})
	}
	if !apierrors.IsNotFound(err) {
		zap.L().Error("failed to get global role", zapGetGlobalRole, zap.Error(err))
		return nil, nil, err
	}

	// The name did not match a global role resource, so fall back to matching the display name.
	globalRoles, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "globalrole",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list global roles", zapGetGlobalRole, zap.Error(err))
		return nil, nil, err
	}

	var matched []*unstructured.Unstructured
	for _, gr := range globalRoles {
		if displayName, found, err := unstructured.NestedString(gr.Object, "displayName"); err == nil && found && strings.EqualFold(displayName, params.Name) {
			matched = append(matched, gr)
		}
	}

	switch len(matched) {
	case 0:
		return noGlobalRoleMatchResponse(params.Name, globalRoles)
	case 1:
		return createGlobalRolesResponse(matched)
	default:
		return createGlobalRolesResponse(matched, "multiple global roles have this display name, ask the user which one they mean")
	}
}

func createGlobalRolesResponse(globalRoles []*unstructured.Unstructured, notes ...string) (*mcp.CallToolResult, any, error) {
	mcpResponse, err := response.CreateMcpResponse(globalRoles, "local", notes...)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetGlobalRole, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// noGlobalRoleMatchResponse lists the names and display names of all global roles, so the
// closest match can be suggested to the user without retrieving the rules of every role.
func noGlobalRoleMatchResponse(name string, globalRoles []*unstructured.Unstructured) (*mcp.CallToolResult, any, error) {
	available := make([]globalRoleSummary, 0, len(globalRoles))
	for _, gr := range globalRoles {
		displayName, _, _ := unstructured.NestedString(gr.Object, "displayName")
		available = append(available, globalRoleSummary{Name: gr.GetName(), DisplayName: displayName})
	}

	mcpResponse, err := response.CreateMcpResponseAny(noGlobalRoleMatch{
		Note:                 fmt.Sprintf("no global role has the name or display name %q. Suggest the closest of the available global roles to the user", name),
		AvailableGlobalRoles: available,
	})
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetGlobalRole, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
