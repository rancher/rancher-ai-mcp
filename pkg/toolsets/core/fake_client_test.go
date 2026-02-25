package core

import (
	"context"
	"fmt"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// fakeToolsClient wraps a *client.Client and validates tokens before delegating to the wrapped client.
type fakeToolsClient struct {
	client        *client.Client
	expectedToken string
}

// newFakeToolsClient creates a new fake tools client that validates the token.
// If expectedToken is empty, token validation is skipped.
func newFakeToolsClient(c *client.Client, expectedToken string) *fakeToolsClient {
	return &fakeToolsClient{
		client:        c,
		expectedToken: expectedToken,
	}
}

// validateToken checks if the provided token matches the expected token.
func (f *fakeToolsClient) validateToken(token string) error {
	if f.expectedToken != "" && token != f.expectedToken {
		return fmt.Errorf("invalid token: expected %q, got %q", f.expectedToken, token)
	}
	return nil
}

// GetResource validates the token and delegates to the wrapped client.
func (f *fakeToolsClient) GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}
	return f.client.GetResource(ctx, params)
}

// GetResourceInterface validates the token and delegates to the wrapped client.
func (f *fakeToolsClient) GetResourceInterface(ctx context.Context, token string, url string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error) {
	if err := f.validateToken(token); err != nil {
		return nil, err
	}
	return f.client.GetResourceInterface(ctx, token, url, namespace, cluster, gvr)
}

// GetResources validates the token and delegates to the wrapped client.
func (f *fakeToolsClient) GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}
	return f.client.GetResources(ctx, params)
}

// CreateClientSet validates the token and delegates to the wrapped client.
func (f *fakeToolsClient) CreateClientSet(ctx context.Context, token string, url string, cluster string) (kubernetes.Interface, error) {
	if err := f.validateToken(token); err != nil {
		return nil, err
	}
	return f.client.CreateClientSet(ctx, token, url, cluster)
}

func (f *fakeToolsClient) GetClusterID(ctx context.Context, token string, url string, clusterNameOrID string) (string, error) {
	if err := f.validateToken(token); err != nil {
		return "", err
	}
	return f.client.GetClusterID(ctx, token, url, clusterNameOrID)
}
