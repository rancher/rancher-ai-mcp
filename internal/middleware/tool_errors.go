package middleware

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Hints for how to fix rejected credentials, for each way of supplying them.
const (
	// StdioCredentialsHint is for stdio mode, where every call uses the token the server was started with.
	StdioCredentialsHint = "Replace the configured Rancher token (--rancher-token or RANCHER_TOKEN) and restart the MCP server."
	// HTTPCredentialsHint is for HTTP mode, where each request brings its own credentials.
	HTTPCredentialsHint = "Sign in again, or send a valid token in the R_token header."
)

// unauthorizedMessage is the start of the message returned in place of an unauthorized tool error.
const unauthorizedMessage = "Rancher rejected the credentials because the token is invalid or has expired, so no tool can be used until they are replaced. "

// ToolErrors returns MCP middleware that gives every tool the same error when Rancher rejects the credentials.
//
// Tools report a rejected token in different ways, and tools that make several requests can repeat
// "Unauthorized" once for each of them. Every one of those errors has the same cause and the same fix,
// so the result of any tool call that failed with a 401 is replaced with a single message ending in hint.
// The original error is logged.
func ToolErrors(hint string) mcp.Middleware {
	message := unauthorizedMessage + hint

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)

			callResult, ok := result.(*mcp.CallToolResult)
			if !ok || !callResult.IsError {
				return result, err
			}

			toolErr := callResult.GetError()
			if !apierrors.IsUnauthorized(toolErr) {
				return result, err
			}

			var tool string
			if callReq, ok := req.(*mcp.CallToolRequest); ok && callReq.Params != nil {
				tool = callReq.Params.Name
			}

			zap.L().Warn("rancher rejected the credentials", zap.String("tool", tool), zap.Error(toolErr))

			callResult.Content = []mcp.Content{&mcp.TextContent{Text: message}}

			return result, err
		}
	}
}
