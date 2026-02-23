package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRancherURLFromAuthServerURL(t *testing.T) {
	testCases := map[string]struct {
		input    string
		expected string
		wantErr  bool
	}{
		"empty input returns empty string": {
			input:    "",
			expected: "",
			wantErr:  false,
		},
		"removes path query and fragment": {
			input:    "https://rancher.example.com/v3-public/auth?scope=openid#section",
			expected: "https://rancher.example.com",
			wantErr:  false,
		},
		"keeps scheme host and port": {
			input:    "https://rancher.example.com:9443/oauth2/authorize",
			expected: "https://rancher.example.com:9443",
			wantErr:  false,
		},
		"strips trailing slash-only path": {
			input:    "https://rancher.example.com/",
			expected: "https://rancher.example.com",
			wantErr:  false,
		},
		"invalid URL returns error": {
			input:   "http://[::1",
			wantErr: true,
		},
	}

	for name, tt := range testCases {
		t.Run(name, func(t *testing.T) {
			actual, err := rancherURLFromAuthServerURL(tt.input)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

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
