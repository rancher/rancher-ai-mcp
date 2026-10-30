package rbac

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestListGlobalRoles(t *testing.T) {
	gr1 := globalRole("admin", map[string]any{"builtin": true})
	gr2 := globalRole("gr-custom", map[string]any{"displayName": "Custom Role"})

	tests := map[string]struct {
		objects        []runtime.Object
		expectedResult string
	}{
		"list all global roles": {
			objects: []runtime.Object{gr1, gr2},
			expectedResult: `{
				"llm": [
					{"apiVersion": "management.cattle.io/v3", "kind": "GlobalRole", "metadata": {"name": "admin"}, "builtin": true},
					{"apiVersion": "management.cattle.io/v3", "kind": "GlobalRole", "metadata": {"name": "gr-custom"}, "displayName": "Custom Role"}
				],
				"uiContext": [
					{"cluster": "local", "kind": "GlobalRole", "name": "admin", "namespace": "", "type": "globalrole"},
					{"cluster": "local", "kind": "GlobalRole", "name": "gr-custom", "namespace": "", "type": "globalrole"}
				]
			}`,
		},
		"no global roles found": {
			objects:        []runtime.Object{},
			expectedResult: `{"llm": "no resources found"}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, _ := newTestTools(t, tt.objects...)

			result, _, err := tools.listGlobalRoles(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				struct{}{},
			)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
