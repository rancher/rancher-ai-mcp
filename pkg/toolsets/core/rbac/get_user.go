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

var zapGetUser = zap.String("tool", "getUser")

type getUserParams struct {
	Username string `json:"username" jsonschema:"the username of the user to retrieve"`
}

func (t *Tools) getUser(ctx context.Context, toolReq *mcp.CallToolRequest, params getUserParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("getUser called")

	matchedUser, err := t.findUser(ctx, params.Username)
	if err != nil {
		zap.L().Error("failed to get users", zapGetUser, zap.Error(err))
		return nil, nil, err
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

// findUser returns the users matching the given username or user ID.
func (t *Tools) findUser(ctx context.Context, username string) ([]*unstructured.Unstructured, error) {
	users, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "user",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		return nil, err
	}

	var matchedUser []*unstructured.Unstructured
	// We return the first user that matches either the username or the metadata.name field.
	// It's possible that a user could have a username that matches another user's metadata.name, but this is unlikely since metadata.name is generated.
	// We will return the first match we find in a best effort approach.
	for _, u := range users {
		if userName, found, err := unstructured.NestedString(u.Object, "username"); err == nil && found && userName == username {
			matchedUser = append(matchedUser, u)
			break
		}
		if metadataName, found, err := unstructured.NestedString(u.Object, "metadata", "name"); err == nil && found && metadataName == username {
			matchedUser = append(matchedUser, u)
			break
		}
	}

	return matchedUser, nil
}
