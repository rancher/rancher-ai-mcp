package cmd

import (
	"strings"

	"github.com/rancher/rancher-ai-mcp/pkg/version"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logLevel string
)

var rootCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Rancher Model Context Protocol (MCP) Server",
	Long: `The MCP server allows the Rancher AI agent to securely retrieve 
or update Kubernetes and Rancher resources across local and downstream clusters.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		initLogger()
	},
	Version: version.GetVersion(),
}

// Execute runs the root command
func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "", "Set the log level (debug, info, warn, error)")
}

func initLogger() {
	var config zap.Config
	if strings.ToLower(logLevel) == "debug" {
		config = zap.NewDevelopmentConfig()
	} else {
		config = zap.NewProductionConfig()
		// remove the "caller" key from the log output
		config.EncoderConfig.CallerKey = zapcore.OmitKey
	}
	// always log to stderr: stdout is reserved for the MCP stdio transport
	config.OutputPaths = []string{"stderr"}
	config.ErrorOutputPaths = []string{"stderr"}
	zap.ReplaceGlobals(zap.Must(config.Build()))
}
