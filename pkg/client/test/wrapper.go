// Package test provides a client wrapper for tool tests that checks each request carries the expected token.
package test

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
)

// ClientWrapper wraps a *client.Client and validates tokens before delegating to the wrapped client.
type ClientWrapper struct {
	client        *client.Client
	expectedToken string
}

// WrapClient creates a new fake tools client that validates the token.
//
// If expectedToken is empty, token validation is skipped.
func WrapClient(c *client.Client, expectedToken string) *ClientWrapper {
	return &ClientWrapper{
		client:        c,
		expectedToken: expectedToken,
	}
}

// NewCallToolRequest creates a new empty *mcp.CallToolRequest.
// The url parameter is accepted for backward compatibility but is ignored.
func NewCallToolRequest(_ string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{}
}

// validateToken checks if the provided token matches the expected token.
func (f *ClientWrapper) validateToken(token string) error {
	if f.expectedToken != "" && token != f.expectedToken {
		return fmt.Errorf("invalid token: expected %q, got %q", f.expectedToken, token)
	}

	return nil
}

// RancherURL delegates to the wrapped client.
func (f *ClientWrapper) RancherURL() string {
	return f.client.RancherURL()
}

// CreateRestConfig delegates to the wrapped client.
func (f *ClientWrapper) CreateRestConfig(token string, clusterID string) (*rest.Config, error) {
	return f.client.CreateRestConfig(token, clusterID)
}

// GetResource validates the token and delegates to the wrapped client.
func (f *ClientWrapper) GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResource(ctx, params)
}

// GetResourceInterface validates the token and delegates to the wrapped client.
func (f *ClientWrapper) GetResourceInterface(ctx context.Context, token string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error) {
	if err := f.validateToken(token); err != nil {
		return nil, err
	}

	return f.client.GetResourceInterface(ctx, token, namespace, cluster, gvr)
}

// GetResources validates the token and delegates to the wrapped client.
func (f *ClientWrapper) GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResources(ctx, params)
}

// CreateClientSet validates the token and delegates to the wrapped client.
func (f *ClientWrapper) CreateClientSet(ctx context.Context, token string, cluster string) (kubernetes.Interface, error) {
	if err := f.validateToken(token); err != nil {
		return nil, err
	}

	return f.client.CreateClientSet(ctx, token, cluster)
}

// GetResourceAtAnyAPIVersion validates the token and delegates to the wrapped client.
func (f *ClientWrapper) GetResourceAtAnyAPIVersion(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResourceAtAnyAPIVersion(ctx, params)
}

// GetResourceByGVR validates the token and delegates to the wrapped client.
func (f *ClientWrapper) GetResourceByGVR(ctx context.Context, params client.GetParams, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResourceByGVR(ctx, params, gvr)
}

// GetResourcesAtAnyAPIVersion validates the token and delegates to the wrapped client.
func (f *ClientWrapper) GetResourcesAtAnyAPIVersion(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResourcesAtAnyAPIVersion(ctx, params)
}

// GetClusterID validates the token and delegates to the wrapped client.
func (f *ClientWrapper) GetClusterID(ctx context.Context, token string, clusterNameOrID string) (string, error) {
	if err := f.validateToken(token); err != nil {
		return "", err
	}

	return f.client.GetClusterID(ctx, token, clusterNameOrID)
}

// GetRancherURL delegates to the wrapped client without validating the token, since some Rancher
// endpoints, such as the public ones, are called without one.
func (f *ClientWrapper) GetRancherURL(ctx context.Context, token string, method string, rancherPath string, body any, v any) error {
	return f.client.GetRancherURL(ctx, token, method, rancherPath, body, v)
}
