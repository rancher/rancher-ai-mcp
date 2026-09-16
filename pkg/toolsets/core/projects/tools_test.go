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

func startProjectsMCPServer(t *testing.T, tools *Tools) (*mcp.ClientSession, func()) {
	t.Helper()

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1.0.0"}, nil)
	tools.AddTools(mcpServer)

	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{Handler: handler}
	go server.Serve(listener)

	var session *mcp.ClientSession
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: "http://" + listener.Addr().String()}
	assert.Eventually(t, func() bool {
		session, err = mcpClient.Connect(context.Background(), transport, nil)
		return err == nil
	}, 2*time.Second, 100*time.Millisecond)

	require.NotNil(t, session)
	return session, func() {
		session.Close()
		server.Shutdown(context.Background())
		listener.Close()
	}
}

func TestAddTools(t *testing.T) {
	c, err := client.NewClient(true, "https://localhost:8080")
	require.NoError(t, err)
	session, cleanup := startProjectsMCPServer(t, NewTools(c, false))
	defer cleanup()

	toolsResult, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 6, "incorrect number of project tools registered")

	toolNames := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		toolNames[tool.Name] = true
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
	}
	assert.True(t, toolNames["moveNamespace"])
}

func TestAddToolsReadOnly(t *testing.T) {
	c, err := client.NewClient(true, "https://localhost:8080")
	require.NoError(t, err)
	session, cleanup := startProjectsMCPServer(t, NewTools(c, true))
	defer cleanup()

	toolsResult, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 3, "incorrect number of read-only project tools registered")

	for _, tool := range toolsResult.Tools {
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
		assert.NotEqual(t, "moveNamespace", tool.Name)
	}
}
