package fleet

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestAnalyzeFleetResources(t *testing.T) {
	fakeURL := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		analyzer       *fakeResourceAnalyzer
		requestURL     string
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"returns report on success using request URL": {
			analyzer:       &fakeResourceAnalyzer{report: "fleet is healthy"},
			requestURL:     fakeURL,
			expectedResult: "fleet is healthy",
		},
		"returns report on success using configured rancherURL": {
			analyzer:       &fakeResourceAnalyzer{report: "2 bundles not ready"},
			rancherURL:     fakeURL,
			expectedResult: "2 bundles not ready",
		},
		"error from resourceAnalyzer is propagated": {
			analyzer:      &fakeResourceAnalyzer{err: errors.New("cluster unreachable")},
			requestURL:    fakeURL,
			expectedError: "cluster unreachable",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := &Tools{
				RancherURL:       tt.rancherURL,
				resourceAnalyzer: tt.analyzer,
			}
			req := test.NewCallToolRequest(tt.requestURL)

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
