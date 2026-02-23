package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/dynamiclistener"
	"github.com/rancher/dynamiclistener/server"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/toolsets"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
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

	serveCmd.Flags().StringVar(&authzServerURL, "authz-server-url", "", "Authorization Server URL - used to generate the OIDC urls")
	serveCmd.Flags().StringVar(&jwksURL, "jwks-url", "", "JWKS URL - from the OAuth2 server")
	serveCmd.Flags().StringVar(&resourceURL, "resource-url", "", "Resource URL for this server - this should be the address to access the MCP server")
}

func rancherURLFromAuthServerURL(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	parsed, err := url.Parse(s)
	if err != nil {
		return "", err
	}

	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String(), nil
}

func runServe(cmd *cobra.Command, args []string) error {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "rancher mcp server", Version: "v1.0.0"}, nil)
	client := client.NewClient(insecure)

	rancherURL, err := rancherURLFromAuthServerURL(authzServerURL)
	if err != nil {
		return fmt.Errorf("parsing authz-server-url: %w", err)
	}

	toolsets.AddAllTools(client, mcpServer, rancherURL)

	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{})

	zap.L().Info("Rancher Server", zap.String("url", rancherURL), zap.String("authzServerURL", authzServerURL))
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
