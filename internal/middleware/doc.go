// Package middleware provides HTTP middleware components for the Rancher AI MCP server.
//
// # OAuth 2.1 Authorization
//
// The primary component is an OAuth 2.1 middleware that validates JWT bearer tokens
// according to the Model Context Protocol specification:
// https://modelcontextprotocol.io/specification/draft/basic/authorization
//
// The middleware performs comprehensive token validation including:
//   - JWT signature verification using JWKS (JSON Web Key Set)
//   - Issuer validation to ensure tokens are from the configured authorization server
//   - Audience validation to verify this resource server is an intended recipient
//   - Scope validation requiring at least one supported scope be present
//   - Expiration checking with configurable clock skew tolerance (10s leeway)
//
// # Usage
//
// Create and configure the OAuth middleware:
//
//	config := middleware.NewOAuthConfig(
//	    "https://auth.example.com",           // Authorization server URL
//	    "https://auth.example.com/jwks.json", // JWKS endpoint
//	    "https://resource.example.com",       // This resource server's URL
//	    []string{"rancher:resources", "rancher:cluster"}, // Supported scopes
//	)
//
//	if err := config.LoadJWKS(); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Wrap your handlers
//	http.Handle("/protected", config.OAuthMiddleware(yourHandler))
//
// # Token Context
//
// After successful authorization, the middleware injects the raw JWT token into the
// request context. Downstream handlers can retrieve it using the Token function:
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    token := middleware.Token(r.Context())
//	    // Use token as needed
//	}
//
// # Protected Resource Metadata
//
// The package also provides a metadata endpoint handler that exposes OAuth 2.0
// Protected Resource Metadata as defined in RFC 8414, including the authorization
// server URL, JWKS URL, resource server URL, and supported scopes:
//
//	http.HandleFunc("/.well-known/oauth-protected-resource",
//	    config.HandleProtectedResourceMetadata)
//
// # Security Considerations
//
// The middleware enforces strict security requirements:
//   - Only RS256 (RSA Signature with SHA-256) signing method is accepted
//   - Token expiration is validated with a 10-second leeway for clock skew
//   - All validation failures result in HTTP 401 Unauthorized responses
//   - Failed validations are logged with structured logging (logrus/zap)
//
// # Scope Validation Strategy
//
// The middleware implements an "any-of" scope validation strategy: the token must
// contain at least one scope from the configured SupportedScopes list. Multiple
// scopes in the token are space-separated as per OAuth 2.0 specification.
//
// If a request is made with R_token then it is passed through without
// validation to support the previous authentication mechanism. When no
// authorization server is configured, a bearer token in the Authorization
// header is passed through the same way, so clients that cannot set custom
// headers can still authenticate with a Rancher token.
package middleware
