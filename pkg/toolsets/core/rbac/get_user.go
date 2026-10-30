package rbac

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapGetUser = zap.String("tool", "getUser")

type getUserParams struct {
	Username string `json:"username" jsonschema:"the username or user ID of the user to retrieve"`
}

func (t *Tools) getUser(ctx context.Context, toolReq *mcp.CallToolRequest, params getUserParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getUser called")

	user, err := t.findUser(ctx, params.Username)
	if err != nil {
		zap.L().Error("failed to get users", zapGetUser, zap.Error(err))
		return nil, nil, err
	}

	var matchedUser []*unstructured.Unstructured
	if user != nil {
		matchedUser = append(matchedUser, user)
	}

	mcpResponse, err := response.CreateMcpResponse(matchedUser, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapGetUser, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// findUser returns the user whose metadata.name or username matches nameOrID, or nil if there is no match.
// A metadata.name match is preferred because it is unique, so a user ID always resolves to that user even
// if another user has it as their username. Usernames are only used if no user has the ID, and an error is
// returned if more than one user has the username, rather than choosing one based on the list order.
func (t *Tools) findUser(ctx context.Context, nameOrID string) (*unstructured.Unstructured, error) {
	users, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "user",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		return nil, err
	}

	var usernameMatches []*unstructured.Unstructured
	for _, u := range users {
		if u.GetName() == nameOrID {
			return u, nil
		}
		if userName, found, err := unstructured.NestedString(u.Object, "username"); err == nil && found && userName == nameOrID {
			usernameMatches = append(usernameMatches, u)
		}
	}

	switch len(usernameMatches) {
	case 0:
		return nil, nil
	case 1:
		return usernameMatches[0], nil
	default:
		ids := make([]string, 0, len(usernameMatches))
		for _, u := range usernameMatches {
			ids = append(ids, u.GetName())
		}
		slices.Sort(ids)
		return nil, fmt.Errorf("more than one user has the username %q, specify one of the user IDs: %s", nameOrID, strings.Join(ids, ", "))
	}
}

// resolveUser returns the user whose username or ID matches nameOrID, or an error if there is no match.
func (t *Tools) resolveUser(ctx context.Context, nameOrID string) (*unstructured.Unstructured, error) {
	user, err := t.findUser(ctx, nameOrID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("user %q not found, specify a username or user ID", nameOrID)
	}

	return user, nil
}

// resolveUserID returns the user ID (metadata.name) of the user whose username or ID matches nameOrID.
func (t *Tools) resolveUserID(ctx context.Context, nameOrID string) (string, error) {
	user, err := t.resolveUser(ctx, nameOrID)
	if err != nil {
		return "", err
	}

	return user.GetName(), nil
}
