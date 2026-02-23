package middleware

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"go.uber.org/zap"
)

// expirationLeeway defines the allowed clock skew when validating token expiration.
const expirationLeeway = 10 * time.Second

// signingMethod defines the JWT signing algorithm accepted by this server.
const signingMethod = "RS256"

// tokenHeader is an alternative header with a token
const tokenHeader = "R_token"

// CORS constants for the protected resource metadata endpoint.
const (
	corsAllowOrigin  = "*"
	corsAllowMethods = "GET, OPTIONS"
	corsAllowHeaders = "Content-Type"
)

var (
	errInvalidToken = errors.New("invalid Bearer token")
	errMissingToken = errors.New("missing authorization header")
)

// NewOAuthConfig creates and returns a new OAuthConfig value.
func NewOAuthConfig(authorizationServerURL, jwksURL, resourceURL string, supportedScopes []string) *OAuthConfig {
	return &OAuthConfig{
		AuthorizationServerURL: authorizationServerURL,
		JwksURL:                jwksURL,
		ResourceURL:            resourceURL,
		SupportedScopes:        supportedScopes,
	}
}

// OAuthConfig holds OAuth configuration.
type OAuthConfig struct {
	// AuthorizationServerURL is the URL Authorization Requests are sent to.
	// https://modelcontextprotocol.io/specification/draft/basic/authorization#authorization-server-location
	AuthorizationServerURL string

	// JwksURL is the URL to fetch the JSON Web Key Set (JWKS) from.
	// See jwks_uri in https://datatracker.ietf.org/doc/html/rfc8414.
	JwksURL string

	// ResourceURL is the user-facing URL for this resource server.
	ResourceURL string

	// SupportedScopes is the list of OAuth 2.0 scopes that this resource server supports.
	// All of the Supported Scopes MUST be in the Auth Token Scope.
	// https://modelcontextprotocol.io/specification/draft/basic/authorization#scope-selection-strategy
	SupportedScopes []string

	// InsecureTLS configures the keyfunc to not validate the TLS connection.
	// This should ONLY be used for testing purposes.
	InsecureTLS bool

	jwks keyfunc.Keyfunc
}

// LoadJWKS initializes the JWKS client.
func (c *OAuthConfig) LoadJWKS(ctx context.Context) error {
	if c.JwksURL == "" {
		return nil
	}

	var override keyfunc.Override
	if c.InsecureTLS {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: c.InsecureTLS},
		}
		override.Client = &http.Client{Transport: tr}
	}
	jwks, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{c.JwksURL}, override)
	if err != nil {
		return fmt.Errorf("failed to create JWKS client: %w", err)
	}
	c.jwks = jwks
	zap.L().Info("Initialized JWKS", zap.String("jwksURL", c.JwksURL))

	return nil
}

// OAuthMiddleware is a middleware that performs OAuth 2.1 authorization.
func (c *OAuthConfig) OAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If the token comes in the header no validation is done, it's passed
		// through directly.
		if token := r.Header.Get(tokenHeader); token != "" {
			next.ServeHTTP(w, r.Clone(WithToken(r.Context(), token)))
			return
		}

		// the Keyfunc is only needed to validate Auth tokens.
		if c.jwks == nil {
			zap.L().Error("JWKS not initialized - call LoadJWKS() before using middleware with Auth tokens")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		token, err := c.extractToken(r)
		if err != nil {
			zap.L().Debug("Failed to extract token from header", zap.Error(err))
			c.sendUnauthorized(w)
			return
		}

		if err := c.validateJWT(token); err != nil {
			zap.L().Debug("Failed to validate token", zap.Error(err))
			c.sendUnauthorized(w)
			return
		}

		// Authorization successful - proceed to next handler providing
		// the token in context.
		next.ServeHTTP(w, r.Clone(WithToken(r.Context(), token)))
	})
}

// extractToken extracts the Bearer token from the Authorization header.
func (c *OAuthConfig) extractToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", errMissingToken
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	if tokenString == authHeader {
		return "", errInvalidToken
	}

	if tokenString == "" {
		return "", errInvalidToken
	}

	return tokenString, nil
}

func (c *OAuthConfig) validateJWT(tokenString string) error {
	token, err := jwt.Parse(tokenString, c.jwks.Keyfunc,
		jwt.WithValidMethods([]string{signingMethod}),
		jwt.WithLeeway(expirationLeeway),
		jwt.WithIssuer(c.AuthorizationServerURL),
	)
	if err != nil {
		zap.L().Error("Failed to parse token", zap.Error(err))
		return errInvalidToken
	}

	if !token.Valid {
		zap.L().Error("Invalid token")
		return errInvalidToken
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		zap.L().Error("Invalid claims type")
		return errInvalidToken
	}

	if !c.validateTokenScopes(claims) {
		zap.L().Error("Insufficient scope")
		return errInvalidToken
	}

	return nil
}

func (c *OAuthConfig) validateTokenScopes(claims jwt.MapClaims) bool {
	rawScopes, ok := claims["scope"].([]any)
	if !ok {
		zap.L().Error("scope claim is not valid", zap.Any("scope", claims["scope"]))
		return false
	}

	tokenScopes := make([]string, len(rawScopes))
	for i, scope := range rawScopes {
		tokenScopes[i] = scope.(string)
	}

	for _, scope := range c.SupportedScopes {
		if !slices.Contains(tokenScopes, scope) {
			return false
		}
	}

	return true
}

// sendUnauthorized sends a 401 response with WWW-Authenticate header.
func (c *OAuthConfig) sendUnauthorized(w http.ResponseWriter) {
	metadataURL, err := url.JoinPath(c.ResourceURL, "/.well-known/oauth-protected-resource")
	if err != nil {
		zap.L().Error("Failed to construct metadata URL", zap.Error(err))
		http.Error(w, "Failed to construct metadata URL", http.StatusInternalServerError)
		return
	}

	w.Header().Set("WWW-Authenticate",
		fmt.Sprintf("Bearer resource_metadata=%q", metadataURL))
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

// HandleProtectedResourceMetadata handles the protected resource metadata endpoint
//
// https://modelcontextprotocol.io/specification/draft/basic/authorization#protected-resource-metadata-discovery-requirements
func (c *OAuthConfig) HandleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	zap.L().Debug("Serving protected metadata")
	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", corsAllowOrigin)
	w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
	w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	metadata := oauthex.ProtectedResourceMetadata{
		Resource:             c.ResourceURL,
		ScopesSupported:      c.SupportedScopes,
		AuthorizationServers: []string{c.AuthorizationServerURL},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metadata); err != nil {
		zap.L().Error("Failed to marshal protected resource metadata", zap.Error(err))
	}
}
