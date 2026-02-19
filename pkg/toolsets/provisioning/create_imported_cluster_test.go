package provisioning

import (
	"context"
	"encoding/json"
	"mcp/pkg/client"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const nameRegex = `^c-[a-z0-9]{5}$`

func TestImportedCluster(t *testing.T) {
	tests := []struct {
		name           string
		fakeClientset  kubernetes.Interface
		fakeDynClient  *dynamicfake.FakeDynamicClient
		params         CreateImportedClusterParams
		expectedError  string
		expectedResult string
	}{
		{
			name:          "valid parameters",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateImportedClusterParams{
				ClusterName:              "test-cluster",
				ClusterDescription:       "A test cluster",
				VersionManagementSetting: "true",
			},
			expectedError: "",
			expectedResult: `{
  "llm" : [ {
    "apiVersion" : "management.cattle.io/v3",
    "kind" : "Cluster",
    "metadata" : {
      "annotations" : {
        "generate-name" : "c-",
        "rancher.io/imported-cluster-version-management" : "true"
      },
      "name" : ""
    },
    "spec" : {
      "description" : "A test cluster",
      "displayName" : "test-cluster",
      "imported" : true
    }
  } ],
  "uiContext" : [ {
    "namespace" : "",
    "kind" : "Cluster",
    "cluster" : "local",
    "name" : "",
    "type" : "management.cattle.io.cluster"
  } ]
}`,
		},
		{
			name:          "false version management",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateImportedClusterParams{
				ClusterName:              "test-cluster",
				ClusterDescription:       "A test cluster",
				VersionManagementSetting: "false",
			},
			expectedError: "",
			expectedResult: `{
  "llm" : [ {
    "apiVersion" : "management.cattle.io/v3",
    "kind" : "Cluster",
    "metadata" : {
      "annotations" : {
        "generate-name" : "c-",
        "rancher.io/imported-cluster-version-management" : "false"
      },
      "name" : ""
    },
    "spec" : {
      "description" : "A test cluster",
      "displayName" : "test-cluster",
      "imported" : true
    }
  } ],
  "uiContext" : [ {
    "namespace" : "",
    "kind" : "Cluster",
    "cluster" : "local",
    "name" : "",
    "type" : "management.cattle.io.cluster"
  } ]
}`,
		},
		{
			name:          "missing version management, uses default",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateImportedClusterParams{
				ClusterName:              "test-cluster",
				ClusterDescription:       "A test cluster",
				VersionManagementSetting: "",
			},
			expectedError: "",
			expectedResult: `{
  "llm" : [ {
    "apiVersion" : "management.cattle.io/v3",
    "kind" : "Cluster",
    "metadata" : {
      "annotations" : {
        "generate-name" : "c-",
        "rancher.io/imported-cluster-version-management" : "system-default"
      },
      "name" : ""
    },
    "spec" : {
      "description" : "A test cluster",
      "displayName" : "test-cluster",
      "imported" : true
    }
  } ],
  "uiContext" : [ {
    "namespace" : "",
    "kind" : "Cluster",
    "cluster" : "local",
    "name" : "",
    "type" : "management.cattle.io.cluster"
  } ]
}`,
		},
		{
			name:          "missing description",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateImportedClusterParams{
				ClusterName:              "test-cluster",
				ClusterDescription:       "",
				VersionManagementSetting: "",
			},
			expectedError: "",
			expectedResult: `{
  "llm" : [ {
    "apiVersion" : "management.cattle.io/v3",
    "kind" : "Cluster",
    "metadata" : {
      "annotations" : {
        "generate-name" : "c-",
        "rancher.io/imported-cluster-version-management" : "system-default"
      },
      "name" : ""
    },
    "spec" : {
      "displayName" : "test-cluster",
	  "description": "",
      "imported" : true
    }
  } ],
  "uiContext" : [ {
    "namespace" : "",
    "kind" : "Cluster",
    "cluster" : "local",
    "name" : "",
    "type" : "management.cattle.io.cluster"
  } ]
}`,
		},
		{
			name:          "missing cluster name",
			fakeClientset: newFakeClientSet(),
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(provisioningSchemes()),
			params: CreateImportedClusterParams{
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

			result, _, err := tools.CreateImportedCluster(context.Background(), &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Name: "createImportedCluster",
				},
				Extra: &mcp.RequestExtra{Header: map[string][]string{urlHeader: {testURL}, tokenHeader: {testToken}}},
			}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
			} else {
				assert.NoError(t, err)

				text, ok := result.Content[0].(*mcp.TextContent)
				assert.Truef(t, ok, "expected type *mcp.TextContent")
				assert.Truef(t, ok, "expected expectedResult to be a JSON string")

				obj := make(map[string]interface{})
				err = json.Unmarshal([]byte(text.Text), &obj)
				assert.NoError(t, err)

				strippedResultBytes, err := json.Marshal(checkAndStripName(t, obj))
				assert.NoError(t, err)

				assert.JSONEq(t, test.expectedResult, string(strippedResultBytes), "expected result does not match actual result")
			}
		})
	}
}

// NB: Imported clusters have a randomly generated name assigned to them.
// In order to compare the actual result with the expected result, we need to
// strip out the name from the actual result, as it will be different every time the test is run.
// This function will validate that the name is in the correct format
// (starts with "c-" followed by 5 random alphanumeric characters) and then empty out the relevant name fields.
func checkAndStripName(t *testing.T, obj map[string]interface{}) map[string]interface{} {
	llmSlice, found, _ := unstructured.NestedSlice(obj, "llm")
	assert.True(t, found)
	assert.Equal(t, len(llmSlice), 1)
	firstItem, ok := llmSlice[0].(map[string]interface{})
	assert.True(t, ok)

	name, found, err := unstructured.NestedString(firstItem, "metadata", "name")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Regexp(t, nameRegex, name, "expected name to match the pattern 'c-' followed by 5 random alphanumeric characters")
	err = unstructured.SetNestedField(firstItem, "", "metadata", "name")
	assert.NoError(t, err)
	llmSlice[0] = firstItem

	uiContextSlice, found, _ := unstructured.NestedSlice(obj, "uiContext")
	assert.True(t, found)
	assert.Equal(t, len(uiContextSlice), 1)
	itemMap, ok := uiContextSlice[0].(map[string]interface{})
	assert.True(t, ok)

	name, ok = itemMap["name"].(string)
	assert.True(t, ok)
	assert.NotNil(t, name)
	assert.Regexp(t, nameRegex, name, "expected name to match the pattern 'c-' followed by 5 random alphanumeric characters")
	itemMap["name"] = ""
	uiContextSlice[0] = itemMap

	obj["llm"] = llmSlice
	obj["uiContext"] = uiContextSlice

	return obj
}
