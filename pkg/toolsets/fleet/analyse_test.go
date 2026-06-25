package fleet

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// fakeFleetClient is a minimal toolsClient implementation for tests.
type fakeFleetClient struct{}

func (f *fakeFleetClient) GetResource(_ context.Context, _ client.GetParams) (*unstructured.Unstructured, error) {
	return nil, nil
}

func (f *fakeFleetClient) GetResources(_ context.Context, _ client.ListParams) ([]*unstructured.Unstructured, error) {
	return nil, nil
}

func (f *fakeFleetClient) CreateRestConfig(_ string, _ string) (*rest.Config, error) {
	return &rest.Config{}, nil
}

// fakeResourceAnalyzer is a test double for the resourceAnalyzer interface.
type fakeResourceAnalyzer struct {
	report string
	err    error
}

func (f *fakeResourceAnalyzer) analyzeFleetResources(_ context.Context, _ *rest.Config, _ string) (string, error) {
	return f.report, f.err
}

func TestAnalyzeFleetResources(t *testing.T) {
	fakeToken := "fakeToken"

	tests := map[string]struct {
		analyzer       *fakeResourceAnalyzer
		expectedResult string
		expectedError  string
	}{
		"returns report on success": {
			analyzer:       &fakeResourceAnalyzer{report: "fleet is healthy"},
			expectedResult: "fleet is healthy",
		},
		"error from resourceAnalyzer is propagated": {
			analyzer:      &fakeResourceAnalyzer{err: errors.New("cluster unreachable")},
			expectedError: "cluster unreachable",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := &Tools{
				resourceAnalyzer: tt.analyzer,
				client:           &fakeFleetClient{},
			}
			req := &mcp.CallToolRequest{}

			result, extra, err := tools.analyzeFleetResources(
				middleware.WithToken(t.Context(), fakeToken),
				req,
				analyzeFleetResourcesParams{},
			)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			assert.Nil(t, extra)
			require.NotNil(t, result)
			require.Len(t, result.Content, 1)
			assert.Equal(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
