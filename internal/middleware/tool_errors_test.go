package middleware

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func TestToolErrors(t *testing.T) {
	const hint = "Fix the token."

	wantUnauthorized := unauthorizedMessage + hint

	tests := map[string]struct {
		toolErr     error
		toolResult  *mcp.CallToolResult
		wantText    string
		wantIsError bool
	}{
		"unauthorized": {
			toolErr:     apierrors.NewUnauthorized("Unauthorized"),
			wantText:    wantUnauthorized,
			wantIsError: true,
		},
		"unauthorized from parallel requests": {
			toolErr:     errors.Join(apierrors.NewUnauthorized("Unauthorized"), apierrors.NewUnauthorized("Unauthorized")),
			wantText:    wantUnauthorized,
			wantIsError: true,
		},
		"wrapped unauthorized from a Rancher URL request": {
			toolErr: fmt.Errorf("listing the caller's own tokens: %w",
				fmt.Errorf("request to https://rancher.example.com/v3/tokens failed: %w",
					apierrors.NewGenericServerResponse(401, "GET", schema.GroupResource{}, "", "", 0, true))),
			wantText:    wantUnauthorized,
			wantIsError: true,
		},
		"unauthorized joined with another error": {
			toolErr:     errors.Join(errors.New("connection refused"), apierrors.NewUnauthorized("Unauthorized")),
			wantText:    wantUnauthorized,
			wantIsError: true,
		},
		"forbidden is left alone": {
			toolErr:     apierrors.NewForbidden(schema.GroupResource{Resource: "users"}, "", errors.New("not allowed")),
			wantText:    `users is forbidden: not allowed`,
			wantIsError: true,
		},
		"other errors are left alone": {
			toolErr:     errors.New("username is required"),
			wantText:    "username is required",
			wantIsError: true,
		},
		"successful results are left alone": {
			toolResult: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
			wantText:   "ok",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := callTool(t, ToolErrors(hint), func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
				return tt.toolResult, nil, tt.toolErr
			})

			assert.Equal(t, tt.wantIsError, result.IsError)
			require.Len(t, result.Content, 1)
			assert.Equal(t, tt.wantText, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

// callTool calls a tool with handler through a server using mw, and returns the result the client receives.
func callTool(t *testing.T, mw mcp.Middleware, handler mcp.ToolHandlerFor[struct{}, any]) *mcp.CallToolResult {
	t.Helper()

	ctx := t.Context()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "test"}, handler)
	server.AddReceivingMiddleware(mw)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "test"})
	require.NoError(t, err)

	return result
}
