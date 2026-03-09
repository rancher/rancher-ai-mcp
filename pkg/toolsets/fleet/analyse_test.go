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

// fakeResourceAnalyser is a test double for the resourceAnalyser interface.
type fakeResourceAnalyser struct {
	report string
	err    error
}

func (f *fakeResourceAnalyser) analiseFleetResources(_ context.Context, _ *rest.Config) (string, error) {
	return f.report, f.err
}

func TestAnalyseFleetResources(t *testing.T) {
	fakeURL := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		analyser       *fakeResourceAnalyser
		requestURL     string
		rancherURL     string
		expectedResult string
		expectedError  string
	}{
		"returns report on success using request URL": {
			analyser:       &fakeResourceAnalyser{report: "fleet is healthy"},
			requestURL:     fakeURL,
			expectedResult: "fleet is healthy",
		},
		"returns report on success using configured rancherURL": {
			analyser:       &fakeResourceAnalyser{report: "2 bundles not ready"},
			rancherURL:     fakeURL,
			expectedResult: "2 bundles not ready",
		},
		"error from resourceAnalyser is propagated": {
			analyser:      &fakeResourceAnalyser{err: errors.New("cluster unreachable")},
			requestURL:    fakeURL,
			expectedError: "cluster unreachable",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools := &Tools{
				RancherURL:       tt.rancherURL,
				resourceAnalyser: tt.analyser,
			}
			req := test.NewCallToolRequest(tt.requestURL)

			result, extra, err := tools.analyseFleetResources(
				middleware.WithToken(t.Context(), fakeToken),
				req,
				struct{}{},
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
