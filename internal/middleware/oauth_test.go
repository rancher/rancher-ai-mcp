package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testAuthServerURL  = "https://auth.example.com"
	testResourceURL    = "https://resource.example.com"
	testScope          = "rancher:cluster"
	testInvalidScope   = "mcp:other"
	testOtherURL       = "https://other.example.com"
	testAnotherURL     = "https://another.example.com"
	testWrongAuthURL   = "https://wrong-auth.example.com"
	testWrongResURL    = "https://wrong-resource.example.com"
	testCustomScope1   = "api:read"
	testCustomScope2   = "api:write"
	testCustomScope3   = "api:admin"
	testCustomReadOnly = "custom:read"
	testCustomWrite    = "custom:write"
	testCustomAdmin    = "custom:admin"
)

var privateKey = mustGenerateRSAKey(2048)

func TestMiddlewareWithLegacyTokenHeaderWhenOAuthDisabled(t *testing.T) {
	config := &OAuthConfig{}
	handler := config.OAuthMiddleware(testHandler())
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("R_token", "test-token")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	expectedBody := "success with token test-token"
	if rr.Body.String() != expectedBody {
		t.Errorf("Expected body %q, got %q", expectedBody, rr.Body)
	}
}

func TestMiddlewareWithNoTokenAndNoOAuth(t *testing.T) {
	config := &OAuthConfig{}
	handler := config.OAuthMiddleware(testHandler())
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	expectedBody := "success with token "
	if rr.Body.String() != expectedBody {
		t.Errorf("Expected body %q, got %q", expectedBody, rr.Body)
	}
}

func TestMiddlewareWithLegacyTokenOAuthConfigured(t *testing.T) {
	// No way to verify the token.
	config := setupTestConfig(t, privateKey)
	handler := config.OAuthMiddleware(testHandler())
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}

	authHeader := rr.Header().Get("WWW-Authenticate")
	if authHeader == "" {
		t.Error("Expected WWW-Authenticate header")
	}
}

func TestOAuthMiddlewareValidToken(t *testing.T) {
	config := setupTestConfig(t, privateKey)
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   config.ResourceURL,
		"scope": []any{testScope},
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
	token := createTestToken(t, privateKey, claims)
	handler := config.OAuthMiddleware(testHandler())
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	expectedBody := "success with token " + token
	if rr.Body.String() != expectedBody {
		t.Errorf("Expected body %q, got %q", expectedBody, rr.Body)
	}
}

func TestOAuthMiddlewareNoAuthorizationHeader(t *testing.T) {
	config := setupTestConfig(t, privateKey)
	handler := config.OAuthMiddleware(testHandler())
	// Create request without Authorization header
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Verify unauthorized response
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}

	authHeader := rr.Header().Get("WWW-Authenticate")
	if authHeader == "" {
		t.Error("Expected WWW-Authenticate header")
	}

	expectedPrefix := "Bearer resource_metadata=\""
	if !strings.HasPrefix(authHeader, expectedPrefix) {
		t.Errorf("Expected WWW-Authenticate header to start with %q, got %q", expectedPrefix, authHeader)
	}

	expectedMetadataURL := config.ResourceURL + "/.well-known/oauth-protected-resource"
	expectedHeader := fmt.Sprintf("Bearer resource_metadata=%q", expectedMetadataURL)
	if authHeader != expectedHeader {
		t.Errorf("Expected WWW-Authenticate header %q, got %q", expectedHeader, authHeader)
	}
}

func TestOAuthMiddlewareInvalidBearerFormat(t *testing.T) {
	config := setupTestConfig(t, privateKey)
	handler := config.OAuthMiddleware(testHandler())

	// Create request with invalid bearer format
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic sometoken")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}
}

func TestOAuthMiddlewareInvalidTokenCases(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	// Generate a key with different algorithm for the wrong algorithm test
	wrongAlgKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	tests := map[string]struct {
		tokenString string
	}{
		"Empty token": {
			tokenString: "",
		},
		"Malformed JWT - not enough parts": {
			tokenString: "invalid.token",
		},
		"Malformed JWT - invalid base64": {
			tokenString: "not-base64.not-base64.not-base64",
		},
		"Invalid JSON in payload": {
			tokenString: "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.bm90IGpzb24.signature",
		},
		"Token signed with HS256": {
			tokenString: createTokenWithWrongAlgorithm(t, jwt.SigningMethodHS256, []byte("secret")),
		},
		"Token signed with different RSA key": {
			tokenString: createTestToken(t, wrongAlgKey, jwt.MapClaims{
				"iss":   config.AuthorizationServerURL,
				"aud":   config.ResourceURL,
				"scope": testScope,
				"exp":   time.Now().Add(1 * time.Hour).Unix(),
				"iat":   time.Now().Unix(),
			}),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			handler := config.OAuthMiddleware(testHandler())
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", "Bearer "+tt.tokenString)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got %d", rr.Code)
			}
		})
	}
}

func TestOAuthMiddlewareInvalidAudienceCases(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	tests := map[string]struct {
		audience interface{} // Can be string, []string, or nil
	}{
		"Missing audience": {
			audience: nil,
		},
		"Audience string without match": {
			audience: testOtherURL,
		},
		"Audience array without match": {
			audience: []string{testOtherURL, testAnotherURL},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			claims := jwt.MapClaims{
				"iss":   config.AuthorizationServerURL,
				"scope": testScope,
				"exp":   time.Now().Add(1 * time.Hour).Unix(),
				"iat":   time.Now().Unix(),
			}

			// Only add audience claim if it's not nil
			if tt.audience != nil {
				claims["aud"] = tt.audience
			}

			token := createTestToken(t, privateKey, claims)
			handler := config.OAuthMiddleware(testHandler())

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got %d", rr.Code)
			}
		})
	}
}

func TestOAuthMiddlewareInvalidIssuerCases(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	tests := map[string]struct {
		issuer string
	}{
		"Missing issuer": {
			issuer: "",
		},
		"Non-matching issuer": {
			issuer: testOtherURL,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			claims := jwt.MapClaims{
				"aud":   config.ResourceURL,
				"scope": testScope,
				"exp":   time.Now().Add(1 * time.Hour).Unix(),
				"iat":   time.Now().Unix(),
			}

			// Only add issuer claim if it's not nil
			if tt.issuer != "" {
				claims["iss"] = tt.issuer
			}

			token := createTestToken(t, privateKey, claims)
			handler := config.OAuthMiddleware(testHandler())

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got %d", rr.Code)
			}
		})
	}
}

func TestOAuthMiddlewareInvalidScope(t *testing.T) {
	config := setupTestConfig(t, privateKey)
	// Create token with wrong scope
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   config.ResourceURL,
		"scope": testInvalidScope,
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}

	token := createTestToken(t, privateKey, claims)

	handler := config.OAuthMiddleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}
}

func TestOAuthMiddlewareExpiredToken(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	// Create expired token
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   config.ResourceURL,
		"scope": testScope,
		"exp":   time.Now().Add(-1 * time.Hour).Unix(),
		"iat":   time.Now().Add(-2 * time.Hour).Unix(),
	}

	token := createTestToken(t, privateKey, claims)

	handler := config.OAuthMiddleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Verify unauthorized response
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}
}

func TestOAuthMiddlewareExpiredWithinLeeway(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	// Token expired 5 seconds ago (within 10s leeway) should still be accepted.
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   config.ResourceURL,
		"scope": []any{testScope},
		"exp":   time.Now().Add(-5 * time.Second).Unix(),
		"iat":   time.Now().Add(-2 * time.Minute).Unix(),
	}

	token := createTestToken(t, privateKey, claims)

	handler := config.OAuthMiddleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200 for token within leeway, got %d", rr.Code)
	}
}

func TestOAuthMiddlewareExpiredBeyondLeeway(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	// Token expired 2 minutes ago (beyond 60s leeway) should be rejected.
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   config.ResourceURL,
		"scope": testScope,
		"exp":   time.Now().Add(-2 * time.Minute).Unix(),
		"iat":   time.Now().Add(-10 * time.Minute).Unix(),
	}

	token := createTestToken(t, privateKey, claims)

	handler := config.OAuthMiddleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for token beyond leeway, got %d", rr.Code)
	}
}

func TestOAuthMiddlewareAudienceAsArray(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	// Create token with audience as array including our resource
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   []string{testOtherURL, config.ResourceURL},
		"scope": []string{testScope},
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}

	token := createTestToken(t, privateKey, claims)

	handler := config.OAuthMiddleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	want := "success with token " + token
	if rr.Body.String() != want {
		t.Errorf("Expected body '%s', got '%s'", want, rr.Body)
	}
}

func TestOAuthMiddlewareMultipleScopes(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	// Create token with multiple scopes including required one
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   config.ResourceURL,
		"scope": []any{"openid", "profile", testScope},
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}

	token := createTestToken(t, privateKey, claims)

	handler := config.OAuthMiddleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}
}

func TestHandleProtectedResourceMetadata(t *testing.T) {
	config := &OAuthConfig{
		AuthorizationServerURL: testAuthServerURL,
		JwksURL:                testAuthServerURL + "/.well-known/jwks.json",
		ResourceURL:            testResourceURL,
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rr := httptest.NewRecorder()

	config.HandleProtectedResourceMetadata(rr, req)

	// Verify response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Verify content type
	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
	}

	// Verify CORS headers
	corsOrigin := rr.Header().Get("Access-Control-Allow-Origin")
	if corsOrigin != "*" {
		t.Errorf("Expected CORS origin '*', got '%s'", corsOrigin)
	}

	// Parse and verify response body
	var metadata map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&metadata)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if metadata["resource"] != config.ResourceURL {
		t.Errorf("Expected resource '%s', got '%v'", config.ResourceURL, metadata["resource"])
	}
}

func TestHandleProtectedResourceMetadataOPTIONS(t *testing.T) {
	config := &OAuthConfig{
		AuthorizationServerURL: testAuthServerURL,
		JwksURL:                testAuthServerURL + "/.well-known/jwks.json",
		ResourceURL:            testResourceURL,
	}

	req := httptest.NewRequest(http.MethodOptions, "/.well-known/oauth-protected-resource", nil)
	rr := httptest.NewRecorder()

	config.HandleProtectedResourceMetadata(rr, req)

	// Verify response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Verify CORS headers are set
	corsOrigin := rr.Header().Get("Access-Control-Allow-Origin")
	if corsOrigin != "*" {
		t.Errorf("Expected CORS origin '*', got '%s'", corsOrigin)
	}
}

func TestValidateScope(t *testing.T) {
	tests := []struct {
		name            string
		supportedScopes []string
		claims          jwt.MapClaims
		valid           bool
	}{
		{
			name:            "Valid scope",
			supportedScopes: []string{testScope},
			claims:          jwt.MapClaims{"scope": []any{testScope}},
			valid:           true,
		},
		{
			name:            "Valid scope with multiple scopes",
			supportedScopes: []string{testScope},
			claims:          jwt.MapClaims{"scope": []any{"openid", "profile", testScope}},
			valid:           true,
		},
		{
			supportedScopes: []string{testScope, testInvalidScope},
			name:            "Invalid scope",
			claims:          jwt.MapClaims{"scope": []any{testInvalidScope}},
			valid:           false,
		},
		{
			name:   "Missing scope",
			claims: jwt.MapClaims{},
			valid:  false,
		},
		{
			name:   "Invalid scope type",
			claims: jwt.MapClaims{"scope": 123},
			valid:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &OAuthConfig{
				SupportedScopes: tt.supportedScopes,
			}
			result := config.validateTokenScopes(tt.claims)
			if result != tt.valid {
				t.Errorf("got validTokenScopes %v, want %v", result, tt.valid)
			}
		})
	}
}

func TestOAuthMiddlewareCustomScopes(t *testing.T) {
	config := setupTestConfig(t, privateKey)
	// Configure custom scopes
	config.SupportedScopes = []string{testCustomScope1, testCustomScope2}

	// Create token with custom scope
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   config.ResourceURL,
		"scope": []any{testCustomScope1, testCustomScope2, "offline_access"},
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}

	token := createTestToken(t, privateKey, claims)

	handler := config.OAuthMiddleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Verify success response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	want := "success with token " + token
	if rr.Body.String() != want {
		t.Errorf("Expected body '%s', got '%s'", want, rr.Body)
	}
}

func TestOAuthMiddlewareCustomScopesInvalid(t *testing.T) {
	config := setupTestConfig(t, privateKey)

	// Configure custom scopes
	config.SupportedScopes = []string{testCustomScope1, testCustomScope2}

	// Create token with scope not in the supported list
	claims := jwt.MapClaims{
		"iss":   config.AuthorizationServerURL,
		"aud":   config.ResourceURL,
		"scope": testScope, // This is not in our custom scopes list
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}

	token := createTestToken(t, privateKey, claims)

	handler := config.OAuthMiddleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Verify unauthorized response
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", rr.Code)
	}
}

func TestHandleProtectedResourceMetadataCustomScopes(t *testing.T) {
	config := &OAuthConfig{
		AuthorizationServerURL: testAuthServerURL,
		JwksURL:                testAuthServerURL + "/.well-known/jwks.json",
		ResourceURL:            testResourceURL,
		SupportedScopes:        []string{testCustomScope1, testCustomScope2, testCustomScope3},
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rr := httptest.NewRecorder()

	config.HandleProtectedResourceMetadata(rr, req)

	// Verify response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Parse and verify response body
	var metadata map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&metadata)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify custom scopes are in the metadata
	scopes, ok := metadata["scopes_supported"].([]interface{})
	if !ok {
		t.Fatal("scopes_supported not found or wrong type")
	}

	expectedScopes := map[string]bool{
		testCustomScope1: false,
		testCustomScope2: false,
		testCustomScope3: false,
	}

	for _, scope := range scopes {
		scopeStr, ok := scope.(string)
		if !ok {
			t.Errorf("Scope is not a string: %v", scope)
			continue
		}
		if _, exists := expectedScopes[scopeStr]; exists {
			expectedScopes[scopeStr] = true
		}
	}

	for scope, found := range expectedScopes {
		if !found {
			t.Errorf("Expected scope '%s' not found in metadata", scope)
		}
	}
}

func TestExtractToken(t *testing.T) {
	config := &OAuthConfig{}

	tests := []struct {
		name       string
		authHeader string
		wantToken  string
		wantErr    error
	}{
		{
			name:       "Valid Bearer token",
			authHeader: "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.test",
			wantToken:  "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.test",
			wantErr:    nil,
		},
		{
			name:       "Missing Authorization header",
			authHeader: "",
			wantToken:  "",
			wantErr:    errMissingToken,
		},
		{
			name:       "Invalid Bearer format - Basic auth",
			authHeader: "Basic dXNlcjpwYXNz",
			wantToken:  "",
			wantErr:    errInvalidToken,
		},
		{
			name:       "Invalid Bearer format - no scheme",
			authHeader: "sometoken",
			wantToken:  "",
			wantErr:    errInvalidToken,
		},
		{
			name:       "Bearer with no token",
			authHeader: "Bearer ",
			wantToken:  "",
			wantErr:    errInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			token, err := config.extractToken(req)

			if err != tt.wantErr {
				t.Errorf("extractToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if token != tt.wantToken {
				t.Errorf("extractToken() token = %v, want %v", token, tt.wantToken)
			}
		})
	}
}

func TestNewOAuthConfig(t *testing.T) {
	authURL := testAuthServerURL
	jwksURL := testAuthServerURL + "/jwks.json"
	resourceURL := testResourceURL
	scopes := []string{"scope1", "scope2"}

	config := NewOAuthConfig(authURL, jwksURL, resourceURL, scopes)

	if config.AuthorizationServerURL != authURL {
		t.Errorf("Expected AuthorizationServerURL %q, got %q", authURL, config.AuthorizationServerURL)
	}
	if config.JwksURL != jwksURL {
		t.Errorf("Expected JwksURL %q, got %q", jwksURL, config.JwksURL)
	}
	if config.ResourceURL != resourceURL {
		t.Errorf("Expected ResourceURL %q, got %q", resourceURL, config.ResourceURL)
	}
	if !reflect.DeepEqual(config.SupportedScopes, scopes) {
		t.Errorf("Expected %v scopes, got %v", scopes, config.SupportedScopes)
	}
}

func TestLoadJWKSEmptyURL(t *testing.T) {
	config := &OAuthConfig{
		JwksURL: "",
	}

	err := config.LoadJWKS(t.Context())

	if err != nil {
		t.Fatal("LoadJWKS with empty URL got an error")
	}
}

func TestOAuthMiddlewareJWKSNotLoaded(t *testing.T) {
	// Create config without loading JWKS
	config := &OAuthConfig{
		AuthorizationServerURL: testAuthServerURL,
		JwksURL:                testAuthServerURL + "/jwks.json",
		ResourceURL:            testResourceURL,
		SupportedScopes:        []string{testScope},
		// jwks is nil - not loaded
	}

	handler := config.OAuthMiddleware(testHandler())
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500 when JWKS not loaded, got %d", rr.Code)
	}
}

func TestSendUnauthorizedURLJoinError(t *testing.T) {
	// Create config with invalid ResourceURL that will cause url.JoinPath to fail
	config := &OAuthConfig{
		ResourceURL: "://invalid-url", // Invalid URL scheme
	}

	rr := httptest.NewRecorder()
	config.sendUnauthorized(rr)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500 for URL join error, got %d", rr.Code)
	}
}

func TestHandleProtectedResourceMetadataCORSHeaders(t *testing.T) {
	config := &OAuthConfig{
		AuthorizationServerURL: testAuthServerURL,
		JwksURL:                testAuthServerURL + "/.well-known/jwks.json",
		ResourceURL:            testResourceURL,
		SupportedScopes:        []string{"test:scope"},
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rr := httptest.NewRecorder()

	config.HandleProtectedResourceMetadata(rr, req)

	// Verify CORS headers are set correctly
	if origin := rr.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("Expected CORS origin %q, got %q", "*", origin)
	}
	if methods := rr.Header().Get("Access-Control-Allow-Methods"); methods != "GET, OPTIONS" {
		t.Errorf("Expected CORS methods %q, got %q", "GET, OPTIONS", methods)
	}
	if headers := rr.Header().Get("Access-Control-Allow-Headers"); headers != "Content-Type" {
		t.Errorf("Expected CORS headers %q, got %q", "Content-Type", headers)
	}
}

func testHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := Token(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success with token " + token))
	})
}

// Fake JWKS server for testing - handles all requests by returning the provided
// rsa.PrivateKey as a JWK - DOES NOT CARE ABOUT THE REQUESTED URL.
func createFakeJWKSServer(t *testing.T, privateKey *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate JWK from the private key
		jwk := map[string]interface{}{
			"kty": "RSA",
			"kid": "test-key-id",
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(bigIntToBytes(int64(privateKey.E))),
		}

		jwks := map[string]interface{}{
			"keys": []interface{}{jwk},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(jwks); err != nil {
			t.Fatalf("encoding the jwks: %s", err)
		}
	}))

	t.Cleanup(srv.Close)

	return srv
}

func bigIntToBytes(n int64) []byte {
	return big.NewInt(n).Bytes()
}

// createTestToken creates a JWT token for testing
func createTestToken(t *testing.T, privateKey *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key-id"

	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("Failed to sign token: %v", err)
	}

	return tokenString
}

// createTokenWithWrongAlgorithm creates a JWT token signed with a non-RS256 algorithm
func createTokenWithWrongAlgorithm(t *testing.T, method jwt.SigningMethod, key interface{}) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":   testAuthServerURL,
		"aud":   testResourceURL,
		"scope": testScope,
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
	token := jwt.NewWithClaims(method, claims)

	tokenString, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("Failed to sign token: %v", err)
	}

	return tokenString
}

// setupTestConfig creates a test OAuth config with a fake server JWKS server
func setupTestConfig(t *testing.T, privateKey *rsa.PrivateKey) *OAuthConfig {
	t.Helper()
	srv := createFakeJWKSServer(t, privateKey)
	config := &OAuthConfig{
		AuthorizationServerURL: testAuthServerURL,
		JwksURL:                srv.URL,
		ResourceURL:            testResourceURL,
		SupportedScopes:        []string{testScope},
	}

	err := config.LoadJWKS(t.Context())
	if err != nil {
		t.Fatalf("Failed to initialize JWKS: %v", err)
	}

	return config
}

func mustGenerateRSAKey(l int) *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, l)
	if err != nil {
		panic(err)
	}

	return key
}
