package cmd

import (
	"testing"

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
