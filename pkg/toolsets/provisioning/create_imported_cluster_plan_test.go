package provisioning

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestCreateImportedClusterPlan(t *testing.T) {
	tests := []struct {
		name           string
		fakeClientset  kubernetes.Interface
		fakeDynClient  *dynamicfake.FakeDynamicClient
		params         createImportedClusterParams
		expectedError  string
		expectedResult string
	}{
		{
			name:          "valid parameters",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
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
			name:          "false version management",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
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
			name:          "missing version management, uses default",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
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
			name:          "missing description",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
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
			name:          "missing cluster name",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
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
			c := &client.Client{
				ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
					return test.fakeClientset, nil
				},
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return test.fakeDynClient, nil
				},
			}
			tools := Tools{client: c, RancherURL: testURL}

			result, _, err := tools.createImportedClusterPlan(context.Background(), &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Name: "createImportedClusterPlan",
				},
			}, test.params)

			if test.expectedError != "" {
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
