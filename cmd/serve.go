package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/dynamiclistener"
	"github.com/rancher/dynamiclistener/server"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/confirm"
	"github.com/rancher/rancher-ai-mcp/pkg/keepalivegate"
	"github.com/rancher/rancher-ai-mcp/pkg/toolconfig"
	"github.com/rancher/rancher-ai-mcp/pkg/toolsets"
	"github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"
)

const (
	tlsName       = "rancher-mcp-server.cattle-ai-agent-system.svc"
	certNamespace = "cattle-ai-agent-system"
	certName      = "cattle-mcp-tls"
	caName        = "cattle-mcp-ca"
)

var (
	port           int
	insecure       bool
	readOnly       bool
	allowAutoWrite bool
	enableExec     bool
	keepAlive      time.Duration
	authzServerURL string
	jwksURL        string
	resourceURL    string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP server",
	Long:  `Start the MCP server to handle requests from the Rancher AI agent`,
	RunE:  runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().IntVar(&port, "port", 9092, "Port to listen on")
	serveCmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification")
	serveCmd.Flags().BoolVar(&readOnly, "read-only", false, "Only register read-only tools")
	serveCmd.Flags().BoolVar(&allowAutoWrite, "allow-auto-write", false, "Allow create/update-class tools to execute without per-operation user confirmation (env MCP_ALLOW_AUTO_WRITE). Delete and exec always require confirmation. DANGEROUS: enable only for trusted automation")
	serveCmd.Flags().BoolVar(&enableExec, "enable-exec", false, "Enable the execPod operation of planChange/executeChange (env MCP_ENABLE_EXEC). The tool surface does not change: without this flag the operation is refused at runtime. Disabled by default")
	serveCmd.Flags().DurationVar(&keepAlive, "keep-alive", 0, "Interval for server keepalive pings (env MCP_KEEP_ALIVE). Pings are only emitted while a confirmation elicitation is open for that session, where they hold the response's SSE stream through proxy/ingress idle timeouts; at all other times the gate swallows them so clients that never open the standalone SSE stream (and cannot pong) are not disconnected. Pick an interval comfortably below the proxy's idle timeout (e.g. 30s). 0 disables.")

	serveCmd.Flags().StringVar(&authzServerURL, "authz-server-url", "", "Authorization Server URL - used to generate the OIDC urls")
	serveCmd.Flags().StringVar(&jwksURL, "jwks-url", "", "JWKS URL - from the OAuth2 server")
	serveCmd.Flags().StringVar(&resourceURL, "resource-url", "", "Resource URL for this server - this should be the address to access the MCP server")
}

func runServe(cmd *cobra.Command, args []string) error {
	allowAutoWrite = boolFlagOrEnv(cmd, "allow-auto-write", "MCP_ALLOW_AUTO_WRITE", allowAutoWrite)
	enableExec = boolFlagOrEnv(cmd, "enable-exec", "MCP_ENABLE_EXEC", enableExec)
	keepAlive = durationFlagOrEnv(cmd, "keep-alive", "MCP_KEEP_ALIVE", keepAlive)

	cfg, err := serveConfig(readOnly, allowAutoWrite, enableExec)
	if err != nil {
		return err
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "rancher mcp server", Version: "v1.0.0"}, &mcp.ServerOptions{
		Instructions: toolsets.SafetyInstructions(cfg),
		KeepAlive:    keepAlive,
	})
	// Gate server->client keepalive pings on pending elicitations: pings only
	// go on the wire while a confirmation dialog is open (holding that POST's
	// SSE stream through proxy idle timeouts). Every other session's pings are
	// swallowed, so clients that never open the standalone SSE stream — and
	// therefore cannot pong — no longer have their sessions destroyed by the
	// SDK's one-missed-pong session.Close().
	mcpServer.AddSendingMiddleware(keepalivegate.Middleware())
	client, err := client.NewClient(insecure, authzServerURL)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	toolsets.AddAllTools(client, mcpServer, cfg)

	warnStartupModes(readOnly, allowAutoWrite, enableExec)

	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{})

	oauthConfig := middleware.NewOAuthConfig(authzServerURL, jwksURL, resourceURL, []string{"offline_access", "rancher:mcp"})
	if insecure {
		oauthConfig.InsecureTLS = true
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", oauthConfig.HandleProtectedResourceMetadata)
	mux.Handle("/", oauthConfig.OAuthMiddleware(handler))

	if err := oauthConfig.LoadJWKS(cmd.Context()); err != nil {
		log.Fatalf("failed to load JWKS: %s", err)
	}

	if insecure {
		return startInsecureServer(mux)
	}

	return startTLSServer(mux)
}

// warnStartupModes logs the safety-relevant startup modes. The auto-write
// warning is gated on !readOnly: read-only mode registers no write tools at
// all, so claiming create/update-class tools will execute without confirmation
// would be false (read-only has the highest precedence).
func warnStartupModes(readOnly, allowAutoWrite, enableExec bool) {
	if allowAutoWrite && !readOnly {
		zap.L().Warn("AUTO-WRITE MODE ENABLED: create/update-class tools will execute WITHOUT per-operation user confirmation; delete and exec still require confirmation")
	}
	zap.L().Info("read-only mode", zap.Bool("enabled", readOnly))
	zap.L().Info("exec tools", zap.Bool("enabled", enableExec))
}

// serveConfig builds the tool configuration the server runs with. It always
// injects a confirmation gate: every mutating tool dereferences it, so a nil
// gate would panic (plan tools) or fail closed (execute tools) in production.
func serveConfig(readOnly, allowAutoWrite, enableExec bool) (toolconfig.Config, error) {
	gate, err := confirm.NewGate()
	if err != nil {
		return toolconfig.Config{}, fmt.Errorf("failed to initialize confirmation gate: %w", err)
	}
	return toolconfig.Config{ReadOnly: readOnly, AutoWrite: allowAutoWrite, EnableExec: enableExec, Gate: gate}, nil
}

// boolFlagOrEnv resolves a boolean startup option. An explicitly set flag
// always wins; otherwise the environment variable is used when it parses as a
// boolean; otherwise the flag's default applies.
func boolFlagOrEnv(cmd *cobra.Command, flagName, envName string, flagVal bool) bool {
	if cmd.Flags().Changed(flagName) {
		return flagVal
	}
	if v := os.Getenv(envName); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return flagVal
}

// durationFlagOrEnv resolves a duration startup option. An explicitly set flag
// always wins; otherwise the environment variable is used when it parses as a
// Go duration (e.g. "30s"); otherwise the flag's default applies.
func durationFlagOrEnv(cmd *cobra.Command, flagName, envName string, flagVal time.Duration) time.Duration {
	if cmd.Flags().Changed(flagName) {
		return flagVal
	}
	if v := os.Getenv(envName); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return flagVal
}

func startInsecureServer(handler http.Handler) error {
	zap.L().Info("MCP Server started!", zap.Int("port", port), zap.Bool("insecure", true))

	addr := fmt.Sprintf(":%d", port)
	return http.ListenAndServe(addr, handler)
}

func startTLSServer(handler http.Handler) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("error creating in-cluster config: %v", err)
	}
	factory, err := core.NewFactoryFromConfig(config)
	if err != nil {
		return fmt.Errorf("creating factory: %v", err)
	}

	ctx := context.Background()
	err = server.ListenAndServe(ctx, port, 0, handler, &server.ListenOpts{
		Secrets:       factory.Core().V1().Secret(),
		CertNamespace: certNamespace,
		CertName:      certName,
		CAName:        caName,
		TLSListenerConfig: dynamiclistener.Config{
			SANs: []string{
				tlsName,
			},
			FilterCN: dynamiclistener.OnlyAllow(tlsName),
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				CipherSuites: []uint16{
					tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
					tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
					tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
					tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
				},
				ClientAuth: tls.RequestClientCert,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating tls server: %v", err)
	}

	zap.L().Info("MCP Server with TLS started!", zap.Int("port", port))
	<-ctx.Done()

	return ctx.Err()
}
