package rbac

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapListUserAttributes = zap.String("tool", "listUserAttributes")

type listUserAttributesParams struct {
	Username string `json:"username,omitempty" jsonschema:"the username or user ID to only return login attributes for a single user. If empty, returns attributes for all users."`
}

// listUserAttributes lists user login attributes (such as last login time), optionally filtered to a single user.
// User attributes are stored under the same resource name as the user they belong to, so the requested username
// is first resolved to a user ID before filtering.
func (t *Tools) listUserAttributes(ctx context.Context, toolReq *mcp.CallToolRequest, params listUserAttributesParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listUserAttributes called")

	userID := params.Username
	if params.Username != "" {
		users, err := t.client.GetResources(ctx, client.ListParams{
			Cluster: "local",
			Kind:    "user",
			Token:   middleware.Token(ctx),
		})
		if err != nil {
			zap.L().Error("failed to get users", zapListUserAttributes, zap.Error(err))
			return nil, nil, err
		}

		for _, u := range users {
			if userName, found, err := unstructured.NestedString(u.Object, "username"); err == nil && found && userName == params.Username {
				userID = u.GetName()
				break
			}
			if u.GetName() == params.Username {
				userID = u.GetName()
				break
			}
		}
	}

	userAttributes, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "userattribute",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to list user attributes", zapListUserAttributes, zap.Error(err))
		return nil, nil, err
	}

	if params.Username != "" {
		var filtered []*unstructured.Unstructured
		for _, ua := range userAttributes {
			if ua.GetName() == userID {
				filtered = append(filtered, ua)
				break
			}
		}
		userAttributes = filtered
	}

	mcpResponse, err := response.CreateMcpResponse(userAttributes, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapListUserAttributes, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
