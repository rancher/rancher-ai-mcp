package test

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const urlHeader string = "R_url"

// clientWrapper wraps a *client.Client and validates tokens before delegating to the wrapped client.
type clientWrapper struct {
	client             *client.Client
	expectedToken      string
	expectedRequestURL string
}

// WrapClient creates a new fake tools client that validates the token and
// Rancher URL.
//
// If expectedToken is empty, token validation is skipped.
// If expectedURL is empty, expectedURL validation is skipped.
func WrapClient(c *client.Client, expectedToken, expectedURL string) *clientWrapper {
	return &clientWrapper{
		client:             c,
		expectedToken:      expectedToken,
		expectedRequestURL: expectedURL,
	}
}

// validateToken checks if the provided token matches the expected token.
func (f *clientWrapper) validateToken(token string) error {
	if f.expectedToken != "" && token != f.expectedToken {
		return fmt.Errorf("invalid token: expected %q, got %q", f.expectedToken, token)
	}

	return nil
}

// validateRequestURL checks if the provided URL matches the expected URL.
func (f *clientWrapper) validateRequestURL(requestURL string) error {
	if f.expectedRequestURL == requestURL {
		return nil
	}

	if requestURL == "" {
		return errors.New("no URL for rancher request")
	}

	return fmt.Errorf("invalid requestURL: expected %q, got %q", f.expectedRequestURL, requestURL)
}

// GetResource validates the token and delegates to the wrapped client.
func (f *clientWrapper) GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}
	if err := f.validateRequestURL(params.URL); err != nil {
		return nil, err
	}

	return f.client.GetResource(ctx, params)
}

// GetResourceInterface validates the token and delegates to the wrapped client.
func (f *clientWrapper) GetResourceInterface(ctx context.Context, token string, url string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error) {
	if err := f.validateToken(token); err != nil {
		return nil, err
	}
	if err := f.validateRequestURL(url); err != nil {
		return nil, err
	}

	return f.client.GetResourceInterface(ctx, token, url, namespace, cluster, gvr)
}

// GetResources validates the token and delegates to the wrapped client.
func (f *clientWrapper) GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}
	if err := f.validateRequestURL(params.URL); err != nil {
		return nil, err
	}

	return f.client.GetResources(ctx, params)
}

// CreateClientSet validates the token and delegates to the wrapped client.
func (f *clientWrapper) CreateClientSet(ctx context.Context, token string, url string, cluster string) (kubernetes.Interface, error) {
	if err := f.validateToken(token); err != nil {
		return nil, err
	}
	if err := f.validateRequestURL(url); err != nil {
		return nil, err
	}

	return f.client.CreateClientSet(ctx, token, url, cluster)
}

func (f *clientWrapper) GetResourceAtAnyAPIVersion(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}
	if err := f.validateRequestURL(params.URL); err != nil {
		return nil, err
	}

	return f.client.GetResourceAtAnyAPIVersion(ctx, params)
}

func (f *clientWrapper) GetResourceByGVR(ctx context.Context, params client.GetParams, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}
	if err := f.validateRequestURL(params.URL); err != nil {
		return nil, err
	}

	return f.client.GetResourceByGVR(ctx, params, gvr)
}

func (f *clientWrapper) GetResourcesAtAnyAPIVersion(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}
	if err := f.validateRequestURL(params.URL); err != nil {
		return nil, err
	}

	return f.client.GetResourcesAtAnyAPIVersion(ctx, params)
}

// NewCallToolRequest creates and returns a CallToolRequest.
//
// The R_url header will be set to the requestURL if requestURL is not empty.
func NewCallToolRequest(requestURL string) *mcp.CallToolRequest {
	req := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: map[string][]string{}}}
	if requestURL != "" {
		req.Extra.Header[urlHeader] = []string{requestURL}
	}

	return req
}
