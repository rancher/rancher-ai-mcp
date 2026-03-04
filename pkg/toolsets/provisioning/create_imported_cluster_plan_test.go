package provisioning

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
				ClusterName:              "test-cluster",
				ClusterDescription:       "A test cluster",
				VersionManagementSetting: "true",
			},
			expectedError: "",
			expectedResult: `{
  "payload": {
    "apiVersion": "management.cattle.io/v3",
    "kind": "Cluster",
    "metadata": {
      "annotations": {
        "generate-name": "c-",
        "rancher.io/imported-cluster-version-management": "true"
      },
      "name": ""
    },
    "spec": {
      "description": "A test cluster",
      "displayName": "test-cluster",
      "imported": true
    }
  },
  "resource": {
    "cluster": "local",
    "kind": "Cluster",
    "name": "",
    "namespace": ""
  },
  "type": "create"
}`,
		},
		{
			name:          "false version management",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: createImportedClusterParams{
				ClusterName:              "test-cluster",
				ClusterDescription:       "A test cluster",
				VersionManagementSetting: "false",
			},
			expectedError: "",
			expectedResult: `{
  "payload": {
    "apiVersion": "management.cattle.io/v3",
    "kind": "Cluster",
    "metadata": {
      "annotations": {
        "generate-name": "c-",
        "rancher.io/imported-cluster-version-management": "false"
      },
      "name": ""
    },
    "spec": {
      "description": "A test cluster",
      "displayName": "test-cluster",
      "imported": true
    }
  },
  "resource": {
    "cluster": "local",
    "kind": "Cluster",
    "name": "",
    "namespace": ""
  },
  "type": "create"
}`,
		},
		{
			name:          "missing version management, uses default",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: createImportedClusterParams{
				ClusterName:              "test-cluster",
				ClusterDescription:       "A test cluster",
				VersionManagementSetting: "",
			},
			expectedError: "",
			expectedResult: `{
  "payload": {
    "apiVersion": "management.cattle.io/v3",
    "kind": "Cluster",
    "metadata": {
      "annotations": {
        "generate-name": "c-",
        "rancher.io/imported-cluster-version-management": "system-default"
      },
      "name": ""
    },
    "spec": {
      "description": "A test cluster",
      "displayName": "test-cluster",
      "imported": true
    }
  },
  "resource": {
    "cluster": "local",
    "kind": "Cluster",
    "name": "",
    "namespace": ""
  },
  "type": "create"
}`,
		},
		{
			name:          "missing description",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: createImportedClusterParams{
				ClusterName:              "test-cluster",
				ClusterDescription:       "",
				VersionManagementSetting: "",
			},
			expectedError: "",
			expectedResult: `{
  "payload": {
    "apiVersion": "management.cattle.io/v3",
    "kind": "Cluster",
    "metadata": {
      "annotations": {
        "generate-name": "c-",
        "rancher.io/imported-cluster-version-management": "system-default"
      },
      "name": ""
    },
    "spec": {
      "description": "",
      "displayName": "test-cluster",
      "imported": true
    }
  },
  "resource": {
    "cluster": "local",
    "kind": "Cluster",
    "name": "",
    "namespace": ""
  },
  "type": "create"
}`,
		},
		{
			name:          "missing cluster name",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: createImportedClusterParams{
				ClusterName:              "",
				ClusterDescription:       "whats my name again",
				VersionManagementSetting: "",
			},
			expectedError:  "ClusterName is required",
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
			tools := Tools{client: c}

			result, _, err := tools.createImportedClusterPlan(context.Background(), &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Name: "createImportedClusterPlan",
				},
				Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {testURL}}},
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

				strippedResultBytes, err := json.Marshal(checkAndStripClusterNameOnPlan(t, obj[0]))
				assert.NoError(t, err)

				t.Log(string(strippedResultBytes))

				assert.JSONEq(t, test.expectedResult, string(strippedResultBytes), "expected result does not match actual result")
			}
		})
	}
}

func checkAndStripClusterNameOnPlan(t *testing.T, obj map[string]interface{}) map[string]interface{} {
	resource, found, err := unstructured.NestedMap(obj, "resource")
	assert.NoError(t, err)
	assert.True(t, found, "resource field not found in object")

	name, found, err := unstructured.NestedString(resource, "name")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Regexp(t, nameRegex, name, "expected name to match the pattern 'c-' followed by 5 random alphanumeric characters")
	err = unstructured.SetNestedField(resource, "", "name")
	assert.NoError(t, err)

	obj["resource"] = resource

	payload, found, err := unstructured.NestedMap(obj, "payload")
	assert.NoError(t, err)
	assert.True(t, found, "payload field not found in object")

	name, found, err = unstructured.NestedString(payload, "metadata", "name")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Regexp(t, nameRegex, name, "expected name to match the pattern 'c-' followed by 5 random alphanumeric characters")
	err = unstructured.SetNestedField(payload, "", "metadata", "name")
	assert.NoError(t, err)

	obj["payload"] = payload

	return obj
}
