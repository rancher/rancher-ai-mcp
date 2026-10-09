package cmd

import (
	"testing"
	"time"

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

// TestKeepAliveFlagPinsDefaultOff pins that SSE keepalive is opt-in: the
// zero default leaves ServerOptions.KeepAlive unset (go-sdk keeps its
// no-heartbeat behavior), so existing deployments see no change unless the
// operator explicitly opts in.
func TestKeepAliveFlagPinsDefaultOff(t *testing.T) {
	flag := serveCmd.Flags().Lookup("keep-alive")
	require.NotNil(t, flag, "--keep-alive must be registered")
	assert.Equal(t, "0s", flag.DefValue, "keep-alive must default to disabled")
	assert.Equal(t, "duration", flag.Value.Type(), "keep-alive must be a duration flag")

	// It must accept a duration value.
	require.NoError(t, serveCmd.Flags().Set("keep-alive", "30s"))
	t.Cleanup(func() { require.NoError(t, serveCmd.Flags().Set("keep-alive", "0s")) })
	assert.Equal(t, 30*time.Second, keepAlive)
}
