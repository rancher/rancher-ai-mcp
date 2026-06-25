package rbac

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
	assert.Empty(t, toolsResult.Tools, "no RBAC tools registered yet")
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
	assert.Empty(t, toolsResult.Tools, "no RBAC tools registered yet")
}
