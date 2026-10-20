package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/dynamiclistener"
	"github.com/rancher/dynamiclistener/server"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/keepalivegate"
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
	authzServerURL string
	jwksURL        string
	resourceURL    string
	stdio          bool
	rancherToken   string
	rancherURL     string
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

	serveCmd.Flags().StringVar(&authzServerURL, "authz-server-url", "", "Authorization Server URL - used to generate the OIDC urls")
	serveCmd.Flags().StringVar(&jwksURL, "jwks-url", "", "JWKS URL - from the OAuth2 server")
	serveCmd.Flags().StringVar(&resourceURL, "resource-url", "", "Resource URL for this server - this should be the address to access the MCP server")

	serveCmd.Flags().BoolVar(&stdio, "stdio", false, "Serve MCP over stdin/stdout instead of HTTP (--port, --authz-server-url, --jwks-url and --resource-url are ignored)")
	serveCmd.Flags().StringVar(&rancherToken, "rancher-token", "", "Rancher token used for all tool calls in stdio mode (defaults to the RANCHER_TOKEN env var)")
	serveCmd.Flags().StringVar(&rancherURL, "rancher-url", "", "Rancher server URL used in stdio mode (defaults to the RANCHER_URL env var)")
}

func runServe(cmd *cobra.Command, args []string) error {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "rancher mcp server", Version: "v1.0.0"}, nil)

	// Install the elicitation-gated keepalive: while a confirmation dialog
	// is open for a session, the gate pings the client every 30s on the
	// real session, holding that POST's SSE response stream through
	// proxy/ingress idle timeouts. When no dialog is open, no pings leave
	// the server at all — so clients that never open the standalone SSE
	// stream (and cannot pong) can never be disconnected for a missed pong.
	// We deliberately do NOT use the SDK's ServerOptions.KeepAlive: its
	// per-session pings are unconditional and it closes the session after
	// one missed pong, which is fatal for POST-only clients.
	mcpServer.AddSendingMiddleware(keepalivegate.Middleware())

	var (
		c     *client.Client
		token string
		err   error
	)
	if stdio {
		var url string
		token, url, err = stdioConfig()
		if err != nil {
			return err
		}
		c, err = client.NewClientWithURL(insecure, url)
	} else {
		c, err = client.NewClient(insecure, authzServerURL)
	}
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	toolsets.AddAllTools(c, mcpServer, readOnly)

	zap.L().Info("read-only mode", zap.Bool("enabled", readOnly))

	if stdio {
		return runStdio(cmd.Context(), mcpServer, token)
	}

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

// stdioConfig returns the Rancher token and URL to use in stdio mode, falling
// back to the RANCHER_TOKEN and RANCHER_URL env vars when the flags are not set.
func stdioConfig() (string, string, error) {
	token := rancherToken
	if token == "" {
		token = os.Getenv("RANCHER_TOKEN")
	}
	url := rancherURL
	if url == "" {
		url = os.Getenv("RANCHER_URL")
	}
	if token == "" || url == "" {
		return "", "", fmt.Errorf("--rancher-token and --rancher-url (or RANCHER_TOKEN and RANCHER_URL) are required when --stdio is set")
	}

	return token, url, nil
}

// runStdio serves MCP over stdin/stdout, using token for all tool calls.
func runStdio(ctx context.Context, mcpServer *mcp.Server, token string) error {
	mcpServer.AddReceivingMiddleware(tokenMiddleware(token))

	// stdout is reserved for the MCP protocol: keep a handle for the transport
	// and redirect any other stdout writes to stderr.
	out := os.Stdout
	os.Stdout = os.Stderr
	transport := &mcp.IOTransport{Reader: os.Stdin, Writer: out}

	zap.L().Info("MCP Server started!", zap.String("transport", "stdio"), zap.Bool("insecure", insecure))

	return mcpServer.Run(ctx, transport)
}

// tokenMiddleware returns an MCP middleware that sets token into the context
// of every request, as the OAuth middleware does for HTTP requests.
func tokenMiddleware(token string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(middleware.WithToken(ctx, token), method, req)
		}
	}
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
