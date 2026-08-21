package rbac

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var zapWhoAmI = zap.String("tool", "whoAmI")

// whoAmI identifies the currently authenticated Rancher user from their token and returns
// their identity along with the matching Rancher user resource, if found.
func (t *Tools) whoAmI(ctx context.Context, toolReq *mcp.CallToolRequest, params struct{}) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("whoAmI called")

	clientset, err := t.client.CreateClientSet(ctx, middleware.Token(ctx), "local")
	if err != nil {
		zap.L().Error("failed to create clientset", zapWhoAmI, zap.Error(err))
		return nil, nil, err
	}

	review, err := clientset.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		zap.L().Error("failed to create self subject review", zapWhoAmI, zap.Error(err))
		return nil, nil, fmt.Errorf("failed to determine current user identity: %w", err)
	}

	userInfo := review.Status.UserInfo

	users, err := t.client.GetResources(ctx, client.ListParams{
		Cluster: "local",
		Kind:    "user",
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		zap.L().Error("failed to get users", zapWhoAmI, zap.Error(err))
		return nil, nil, err
	}

	var matchedUser []*unstructured.Unstructured
	for _, u := range users {
		if u.GetName() == userInfo.Username {
			matchedUser = append(matchedUser, u)
			break
		}
	}

	identity := &unstructured.Unstructured{
		Object: map[string]any{
			"username": userInfo.Username,
			"uid":      userInfo.UID,
			"groups":   userInfo.Groups,
		},
	}

	mcpResponse, err := response.CreateMcpResponse(append([]*unstructured.Unstructured{identity}, matchedUser...), "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapWhoAmI, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
