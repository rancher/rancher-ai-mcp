package provisioning

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rancher/rancher-ai-mcp/pkg/client"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

func TestListSupportedKubernetesVersions(t *testing.T) {
	tests := []struct {
		name           string
		params         ListSupportedK8sVersionsParams
		kdmOutput      string
		expectedError  string
		expectedResult string
	}{
		{
			name:          "invalid distribution",
			params:        ListSupportedK8sVersionsParams{Distribution: "invalid"},
			expectedError: "unsupported distribution: invalid",
		},
		{
			name:           "valid distribution rke2",
			params:         ListSupportedK8sVersionsParams{Distribution: "rke2"},
			kdmOutput:      createDummyKDMData("v1.32.4+rke2r1", "v1.32.3+rke2r1"),
			expectedResult: `{"llm":[{"message":"Supported Kubernetes versions for rke2: [v1.32.4+rke2r1 v1.32.3+rke2r1]"}]}`,
		},
		{
			name:           "valid distribution k3s",
			params:         ListSupportedK8sVersionsParams{Distribution: "k3s"},
			kdmOutput:      createDummyKDMData("v1.32.4+k3s1", "v1.32.3+k3s1"),
			expectedResult: `{"llm":[{"message":"Supported Kubernetes versions for k3s: [v1.32.4+k3s1 v1.32.3+k3s1]"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// setup dummy KDM server
			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(test.kdmOutput))
			}))

			c := &client.Client{}
			tools := Tools{client: c}

			result, _, err := tools.ListSupportedKubernetesVersions(context.Background(), &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Name: "listSupportedKubernetesVersions",
				},
				Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {svr.URL}, tokenHeader: {testToken}}},
			}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				assert.NoError(t, err)

				text, ok := result.Content[0].(*mcp.TextContent)
				assert.Truef(t, ok, "expected type *mcp.TextContent")

				assert.Truef(t, ok, "expected expectedResult to be a JSON string")
				assert.JSONEq(t, test.expectedResult, text.Text)
			}

			svr.Close()
		})
	}
}
