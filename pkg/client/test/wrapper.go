package test

import (
	"context"
	"fmt"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// clientWrapper wraps a *client.Client and validates tokens before delegating to the wrapped client.
type clientWrapper struct {
	client        *client.Client
	expectedToken string
}

// WrapClient creates a new fake tools client that validates the token.
//
// If expectedToken is empty, token validation is skipped.
func WrapClient(c *client.Client, expectedToken string) *clientWrapper {
	return &clientWrapper{
		client:        c,
		expectedToken: expectedToken,
	}
}

// validateToken checks if the provided token matches the expected token.
func (f *clientWrapper) validateToken(token string) error {
	if f.expectedToken != "" && token != f.expectedToken {
		return fmt.Errorf("invalid token: expected %q, got %q", f.expectedToken, token)
	}

	return nil
}

func (f *clientWrapper) RancherURL() string {
	return f.client.RancherURL()
}

func (f *clientWrapper) CreateRestConfig(token string, clusterID string) (*rest.Config, error) {
	return f.client.CreateRestConfig(token, clusterID)
}

// GetResource validates the token and delegates to the wrapped client.
func (f *clientWrapper) GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResource(ctx, params)
}

// GetResourceInterface validates the token and delegates to the wrapped client.
func (f *clientWrapper) GetResourceInterface(ctx context.Context, token string, namespace string, cluster string, gvr schema.GroupVersionResource) (dynamic.ResourceInterface, error) {
	if err := f.validateToken(token); err != nil {
		return nil, err
	}

	return f.client.GetResourceInterface(ctx, token, namespace, cluster, gvr)
}

// GetResources validates the token and delegates to the wrapped client.
func (f *clientWrapper) GetResources(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResources(ctx, params)
}

// CreateClientSet validates the token and delegates to the wrapped client.
func (f *clientWrapper) CreateClientSet(ctx context.Context, token string, cluster string) (kubernetes.Interface, error) {
	if err := f.validateToken(token); err != nil {
		return nil, err
	}

	return f.client.CreateClientSet(ctx, token, cluster)
}

func (f *clientWrapper) GetResourceAtAnyAPIVersion(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResourceAtAnyAPIVersion(ctx, params)
}

func (f *clientWrapper) GetResourceByGVR(ctx context.Context, params client.GetParams, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResourceByGVR(ctx, params, gvr)
}

func (f *clientWrapper) GetResourcesAtAnyAPIVersion(ctx context.Context, params client.ListParams) ([]*unstructured.Unstructured, error) {
	if err := f.validateToken(params.Token); err != nil {
		return nil, err
	}

	return f.client.GetResourcesAtAnyAPIVersion(ctx, params)
}

func (f *clientWrapper) GetClusterID(ctx context.Context, token string, clusterNameOrID string) (string, error) {
	if err := f.validateToken(token); err != nil {
		return "", err
	}

	return f.client.GetClusterID(ctx, token, clusterNameOrID)
}
