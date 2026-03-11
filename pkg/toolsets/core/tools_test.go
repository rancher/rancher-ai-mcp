package core

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
)

func TestAddTools(t *testing.T) {
	tools := NewTools(client.NewClient(true), "not-used-in-test", false)

	// Create a test MCP server
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "v1.0.0",
	}, nil)
	assert.NotNil(t, mcpServer)

	tools.AddTools(mcpServer)

	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{})

	// Start server on a random available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer listener.Close()

	serverAddr := "http://" + listener.Addr().String()

	server := &http.Server{Handler: handler}
	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(context.Background())

	// Wait for server to be ready by attempting to connect with retries
	ctx := context.Background()
	transport := &mcp.StreamableClientTransport{
		Endpoint: serverAddr,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil)

	var cs *mcp.ClientSession
	assert.Eventually(t, func() bool {
		var err error
		cs, err = client.Connect(ctx, transport, nil)
		return err == nil
	}, 2*time.Second, 100*time.Millisecond, "Server should start within 2 seconds")

	assert.NotNil(t, cs)
	defer cs.Close()

	toolsResult, err := cs.ListTools(ctx, &mcp.ListToolsParams{})

	assert.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 16, "incorrect number of tools registered")
	// assert that all tools have the correct toolset annotation
	for _, tool := range toolsResult.Tools {
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
	}
}

func TestAddToolsReadOnly(t *testing.T) {
	tools := NewTools(client.NewClient(true), "not-used-in-test", true)

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "v1.0.0",
	}, nil)
	assert.NotNil(t, mcpServer)

	tools.AddTools(mcpServer)

	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer listener.Close()

	serverAddr := "http://" + listener.Addr().String()

	server := &http.Server{Handler: handler}
	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(context.Background())

	ctx := context.Background()
	transport := &mcp.StreamableClientTransport{
		Endpoint: serverAddr,
	}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil)

	var cs *mcp.ClientSession
	assert.Eventually(t, func() bool {
		var err error
		cs, err = mcpClient.Connect(ctx, transport, nil)
		return err == nil
	}, 2*time.Second, 100*time.Millisecond, "Server should start within 2 seconds")

	assert.NotNil(t, cs)
	defer cs.Close()

	toolsResult, err := cs.ListTools(ctx, &mcp.ListToolsParams{})

	assert.NoError(t, err)
	assert.Len(t, toolsResult.Tools, 10, "read-only mode should not register mutating tools")

	toolNames := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		toolNames[tool.Name] = true
		assert.Equal(t, toolsSet, tool.Meta[toolsSetAnn])
	}
	assert.False(t, toolNames["patchKubernetesResource"], "patchKubernetesResource should not be registered in read-only mode")
	assert.False(t, toolNames["patchKubernetesResourcePlan"], "patchKubernetesResourcePlan should not be registered in read-only mode")
	assert.False(t, toolNames["createKubernetesResource"], "createKubernetesResource should not be registered in read-only mode")
	assert.False(t, toolNames["createKubernetesResourcePlan"], "createKubernetesResourcePlan should not be registered in read-only mode")
	assert.False(t, toolNames["createProject"], "createProject should not be registered in read-only mode")
	assert.False(t, toolNames["createProjectPlan"], "createProjectPlan should not be registered in read-only mode")
}
