package fleet

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// fakeResourceAnalyzer is a test double for the resourceAnalyzer interface.
type fakeResourceAnalyzer struct {
	report string
	err    error
}

func (f *fakeResourceAnalyzer) analyzeFleetResources(_ context.Context, _ *rest.Config, _ string) (string, error) {
	return f.report, f.err
}

// fakeAnalyseClient implements toolsClient for analyse tests.
type fakeAnalyseClient struct{}

func (f *fakeAnalyseClient) RancherURL() string { return "https://localhost:8080" }
func (f *fakeAnalyseClient) GetResource(_ context.Context, _ client.GetParams) (*unstructured.Unstructured, error) {
	return nil, nil
}
func (f *fakeAnalyseClient) GetResources(_ context.Context, _ client.ListParams) ([]*unstructured.Unstructured, error) {
	return nil, nil
}
func (f *fakeAnalyseClient) CreateRestConfig(_ string, _ string) (*rest.Config, error) {
	return &rest.Config{}, nil
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
		"returns report on success using configured rancherURL": {
			analyzer:       &fakeResourceAnalyzer{report: "2 bundles not ready"},
			expectedResult: "2 bundles not ready",
		},
		"error from resourceAnalyzer is propagated": {
			analyzer:      &fakeResourceAnalyzer{err: errors.New("cluster unreachable")},
			expectedError: "cluster unreachable",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := &Tools{
				client:           &fakeAnalyseClient{},
				resourceAnalyzer: tt.analyzer,
			}
			req := test.NewCallToolRequest()

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
