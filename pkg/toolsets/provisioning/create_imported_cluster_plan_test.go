package provisioning

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestCreateImportedClusterPlan(t *testing.T) {
	tests := []struct {
		name           string
		objects        []runtime.Object
		params         createImportedClusterParams
		expectedError  string
		expectedResult string
	}{
		{
			name:    "valid parameters",
			objects: nil,
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				Description:              "A test cluster",
				VersionManagementSetting: "true",
			},
			expectedError: "",
			expectedResult: `{
  "payload": {
    "apiVersion": "management.cattle.io/v3",
    "description": "A test cluster",
    "kind": "Cluster",
    "metadata": {
      "annotations": {
        "rancher.io/imported-cluster-version-management": "true"
      },
      "name": "test-cluster",
      "namespace": "fleet-default"
    },
    "name": "test-cluster",
    "type": "cluster"
  },
  "resource": {
    "cluster": "local",
    "kind": "Cluster",
    "name": "test-cluster",
    "namespace": "fleet-default"
  },
  "type": "create"
}`,
		},
		{
			name:    "false version management",
			objects: nil,
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				Description:              "A test cluster",
				VersionManagementSetting: "false",
			},
			expectedError: "",
			expectedResult: `{
  "payload": {
    "apiVersion": "management.cattle.io/v3",
    "description": "A test cluster",
    "kind": "Cluster",
    "metadata": {
      "annotations": {
        "rancher.io/imported-cluster-version-management": "false"
      },
      "name": "test-cluster",
      "namespace": "fleet-default"
    },
    "name": "test-cluster",
    "type": "cluster"
  },
  "resource": {
    "cluster": "local",
    "kind": "Cluster",
    "name": "test-cluster",
    "namespace": "fleet-default"
  },
  "type": "create"
}`,
		},
		{
			name:    "missing version management, uses default",
			objects: nil,
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				Description:              "A test cluster",
				VersionManagementSetting: "",
			},
			expectedError: "",
			expectedResult: `{
  "payload": {
    "apiVersion": "management.cattle.io/v3",
    "description": "A test cluster",
    "kind": "Cluster",
    "metadata": {
      "annotations": {
        "rancher.io/imported-cluster-version-management": "system-default"
      },
      "name": "test-cluster",
      "namespace": "fleet-default"
    },
    "name": "test-cluster",
    "type": "cluster"
  },
  "resource": {
    "cluster": "local",
    "kind": "Cluster",
    "name": "test-cluster",
    "namespace": "fleet-default"
  },
  "type": "create"
}`,
		},
		{
			name:    "missing description",
			objects: nil,
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				Description:              "",
				VersionManagementSetting: "",
			},
			expectedError: "",
			expectedResult: `{
  "payload": {
    "apiVersion": "management.cattle.io/v3",
    "description": "",
    "kind": "Cluster",
    "metadata": {
      "annotations": {
        "rancher.io/imported-cluster-version-management": "system-default"
      },
      "name": "test-cluster",
      "namespace": "fleet-default"
    },
    "name": "test-cluster",
    "type": "cluster"
  },
  "resource": {
    "cluster": "local",
    "kind": "Cluster",
    "name": "test-cluster",
    "namespace": "fleet-default"
  },
  "type": "create"
}`,
		},
		{
			name:    "missing cluster name",
			objects: nil,
			params: createImportedClusterParams{
				Name:                     "",
				Description:              "whats my name again",
				VersionManagementSetting: "",
			},
			expectedError:  "name is required",
			expectedResult: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tools := newTestProvisioningTools(test.objects...)

			result, _, err := tools.createImportedClusterPlan(middleware.WithToken(t.Context(), testToken), newToolRequest("createImportedClusterPlan"), test.params)

			if test.expectedError != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				assert.NoError(t, err)

				text, ok := result.Content[0].(*mcp.TextContent)
				assert.Truef(t, ok, "expected type *mcp.TextContent")
				assert.Truef(t, ok, "expected expectedResult to be a JSON string")

				var obj []map[string]interface{}
				err = json.Unmarshal([]byte(text.Text), &obj)
				require.NoError(t, err)

				resultBytes, err := json.Marshal(obj[0])
				assert.NoError(t, err)

				assert.JSONEq(t, test.expectedResult, string(resultBytes), "expected result does not match actual result")
			}
		})
	}
}
