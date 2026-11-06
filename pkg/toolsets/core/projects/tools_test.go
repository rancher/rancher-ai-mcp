package projects

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startProjectsMCPServer serves the project tools on a random local port and returns a client session connected to it.
// The session and server are closed when the test finishes.
func startProjectsMCPServer(t *testing.T, tools *Tools) *mcp.ClientSession {
	t.Helper()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1.0.0"}, nil)
	tools.AddTools(mcpServer)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{Handler: handler}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		assert.NoError(t, server.Shutdown(context.WithoutCancel(t.Context())))
		assert.ErrorIs(t, <-serveErr, http.ErrServerClosed)
	})

	var session *mcp.ClientSession
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: "http://" + listener.Addr().String()}
	assert.Eventually(t, func() bool {
		session, err = mcpClient.Connect(t.Context(), transport, nil)
		return err == nil
	}, 2*time.Second, 100*time.Millisecond, "server should start within 2 seconds")
	require.NotNil(t, session)
	t.Cleanup(func() { assert.NoError(t, session.Close()) })

	return session
}

func TestAddTools(t *testing.T) {
	c, err := client.NewClient(true, "https://localhost:8080")
	require.NoError(t, err)
	session := startProjectsMCPServer(t, NewTools(c, false))

	toolsResult, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 7, "incorrect number of project tools registered")

	toolNames := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		toolNames[tool.Name] = true
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
	}
	assert.True(t, toolNames["moveNamespace"])
	assert.True(t, toolNames["getClusterResourceUsage"])
}

func TestAddToolsReadOnly(t *testing.T) {
	c, err := client.NewClient(true, "https://localhost:8080")
	require.NoError(t, err)
	session := startProjectsMCPServer(t, NewTools(c, true))

	toolsResult, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 4, "incorrect number of read-only project tools registered")

	for _, tool := range toolsResult.Tools {
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
		assert.NotEqual(t, "moveNamespace", tool.Name)
	}
}
