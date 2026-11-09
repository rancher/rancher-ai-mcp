package cmd

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeCmd(t *testing.T) {
	assert.NotNil(t, serveCmd)
	assert.Equal(t, "serve", serveCmd.Use)
	assert.Equal(t, "Start the MCP server", serveCmd.Short)
	assert.NotNil(t, serveCmd.RunE)
}

func TestRunServeCommand(t *testing.T) {
	// Create a new command instance to avoid modifying the global one
	testCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server",
		Long:  `Start the MCP server to handle requests from the Rancher AI agent`,
	}

	testCmd.Flags().IntVar(&port, "port", 9092, "Port to listen on")
	testCmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification")

	// Verify flags exist and have correct defaults
	portFlag := testCmd.Flags().Lookup("port")
	require.NotNil(t, portFlag)
	assert.Equal(t, "9092", portFlag.DefValue)

	insecureFlag := testCmd.Flags().Lookup("insecure")
	require.NotNil(t, insecureFlag)
	assert.Equal(t, "false", insecureFlag.DefValue)
}

func TestTokenMiddleware(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "token"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: middleware.Token(ctx)}}}, nil, nil
	})
	server.AddReceivingMiddleware(tokenMiddleware("token-xxx"))

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "token"})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "token-xxx", result.Content[0].(*mcp.TextContent).Text)
}

func TestStdioConfig(t *testing.T) {
	tests := map[string]struct {
		flagToken     string
		flagURL       string
		envToken      string
		envURL        string
		expectedToken string
		expectedURL   string
		expectedErr   bool
	}{
		"uses flags": {
			flagToken:     "flag-token",
			flagURL:       "https://flag.example.com",
			envToken:      "env-token",
			envURL:        "https://env.example.com",
			expectedToken: "flag-token",
			expectedURL:   "https://flag.example.com",
		},
		"falls back to env vars": {
			envToken:      "env-token",
			envURL:        "https://env.example.com",
			expectedToken: "env-token",
			expectedURL:   "https://env.example.com",
		},
		"fails without token": {
			flagURL:     "https://flag.example.com",
			expectedErr: true,
		},
		"fails without URL": {
			flagToken:   "flag-token",
			expectedErr: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			rancherToken, rancherURL = test.flagToken, test.flagURL
			t.Cleanup(func() { rancherToken, rancherURL = "", "" })
			t.Setenv("RANCHER_TOKEN", test.envToken)
			t.Setenv("RANCHER_URL", test.envURL)

			token, url, err := stdioConfig()
			if test.expectedErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expectedToken, token)
			assert.Equal(t, test.expectedURL, url)
		})
	}
}

func TestStripUIContext(t *testing.T) {
	tests := map[string]struct {
		text     string
		expected string
	}{
		"removes uiContext": {
			text:     `{"llm":[{"kind":"Pod","metadata":{"name":"a"}}],"uiContext":[{"namespace":"ns","kind":"Pod","cluster":"local","name":"a"}]}`,
			expected: `{"llm":[{"kind":"Pod","metadata":{"name":"a"}}]}`,
		},
		"no uiContext is unchanged": {
			text:     `{"llm":"no resources found"}`,
			expected: `{"llm":"no resources found"}`,
		},
		"non JSON text is unchanged": {
			text:     "something went wrong",
			expected: "something went wrong",
		},
		"JSON array is unchanged": {
			text:     `[{"uiContext":[]}]`,
			expected: `[{"uiContext":[]}]`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.expected, stripUIContext(test.text))
		})
	}
}

func TestStripUIContextMiddleware(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "resources"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		text, err := response.CreateMcpResponseAny("data", response.UIContext{Namespace: "ns", Kind: "Pod", Cluster: "local", Name: "a"})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, err
	})
	server.AddReceivingMiddleware(stripUIContextMiddleware())

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer cs.Close()

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "resources"})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	assert.Equal(t, `{"llm":"data"}`, result.Content[0].(*mcp.TextContent).Text)
}
