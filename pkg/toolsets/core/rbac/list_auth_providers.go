package rbac

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/rancher/rancher-ai-mcp/pkg/response"
)

var zapListAuthProviders = zap.String("tool", "listAuthProviders")

// publicAuthProvidersPath lists the enabled authentication providers. Rancher serves it without
// authentication so the login page can show them, and ignores any token sent with the request.
const publicAuthProvidersPath = "/v3-public/authProviders"

// authProvider is an enabled authentication provider.
type authProvider struct {
	// Name is the provider's name, which is also the prefix of its principal IDs, e.g. keycloakoidc for keycloakoidc_user://.
	Name string `json:"name"`
	// Type is Rancher's type for the provider, e.g. keyCloakOIDCProvider or openLdapProvider.
	Type string `json:"type"`
}

// listAuthProvidersResult is the response of listAuthProviders.
type listAuthProvidersResult struct {
	Providers []authProvider `json:"providers"`
	Note      string         `json:"note"`
}

// listAuthProviders lists the authentication providers enabled in Rancher.
//
// It uses Rancher's public provider list rather than the AuthConfig resources, so it works for every
// caller, including users who are not allowed to read AuthConfigs and callers whose token is invalid.
func (t *Tools) listAuthProviders(ctx context.Context, toolReq *mcp.CallToolRequest, params struct{}) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listAuthProviders called")

	var collection struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
	}
	// No token is needed, and sending an invalid one would only risk a confusing error.
	if err := t.client.GetRancherURL(ctx, "", http.MethodGet, publicAuthProvidersPath, nil, &collection); err != nil {
		zap.L().Error("failed to list auth providers", zapListAuthProviders, zap.Error(err))
		return nil, nil, fmt.Errorf("listing the enabled authentication providers: %w", err)
	}

	result := listAuthProvidersResult{
		Providers: make([]authProvider, 0, len(collection.Data)),
		Note:      "Only the providers listed are enabled. The local provider can be disabled, so users can only log in with a Rancher username and password if local is listed.",
	}
	for _, p := range collection.Data {
		result.Providers = append(result.Providers, authProvider{Name: p.ID, Type: p.Type})
	}

	mcpResponse, err := response.CreateMcpResponseAny(result)
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListAuthProviders, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
