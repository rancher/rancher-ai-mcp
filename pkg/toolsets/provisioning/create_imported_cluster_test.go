package provisioning

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rancher/rancher-ai-mcp/pkg/client"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestCreateImportedCluster(t *testing.T) {
	tests := []struct {
		name          string
		params        createImportedClusterParams
		serverStatus  int
		serverBody    string
		serverClosed  bool // simulate an unreachable server
		expectedError string
	}{
		{
			name: "non-201 status code returns error",
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				VersionManagementSetting: "true",
			},
			serverStatus:  http.StatusConflict,
			serverBody:    `{"message":"cluster already exists"}`,
			expectedError: "received non-success status code 409",
		},
		{
			name: "500 internal server error returns error",
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				VersionManagementSetting: "system-default",
			},
			serverStatus:  http.StatusInternalServerError,
			serverBody:    `{"message":"internal server error"}`,
			expectedError: "received non-success status code 500",
		},
		{
			name: "invalid JSON in response body returns error",
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				VersionManagementSetting: "true",
			},
			serverStatus:  http.StatusCreated,
			serverBody:    `not valid json`,
			expectedError: "failed to unmarshal response body from Rancher API after cluster creation",
		},
		{
			name: "unreachable server returns error",
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				VersionManagementSetting: "true",
			},
			serverClosed:  true,
			expectedError: "failed to make request to Rancher API to create imported cluster",
		},
		{
			name: "missing cluster name returns error before API call",
			params: createImportedClusterParams{
				Name:                     "",
				Description:              "no name cluster",
				VersionManagementSetting: "",
			},
			serverStatus:  http.StatusCreated,
			serverBody:    `{}`,
			expectedError: "name is required",
		},
		{
			name: "valid response with 201 succeeds",
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				Description:              "A test cluster",
				VersionManagementSetting: "true",
			},
			serverStatus: http.StatusCreated,
			serverBody: `{
				"type": "cluster",
				"name": "test-cluster",
				"state": "provisioning"
			}`,
			expectedError: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := &client.Client{
				ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
					return newFakeClientSet(), nil
				},
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return dynamicfake.NewSimpleDynamicClient(provisioningSchemes()), nil
				},
			}
			tools := Tools{client: c}

			svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.serverStatus)
				w.Write([]byte(test.serverBody))
			}))

			if test.serverClosed {
				svr.Close()
			} else {
				defer svr.Close()
			}

			tools.RancherURL = svr.URL

			result, _, err := tools.createImportedCluster(context.Background(), &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Name: "createImportedCluster",
				},
			}, test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.NotEmpty(t, result.Content)
				_, ok := result.Content[0].(*mcp.TextContent)
				assert.True(t, ok, "expected result content to be *mcp.TextContent")
			}
		})
	}
}

func TestCreateImportedClusterObj(t *testing.T) {
	tools := Tools{}

	tests := []struct {
		name          string
		params        createImportedClusterParams
		expectedError string
		validate      func(t *testing.T, cluster *unstructured.Unstructured)
	}{
		{
			name: "valid parameters with explicit version management true",
			params: createImportedClusterParams{
				Name:                     "test-cluster",
				Description:              "A test cluster",
				VersionManagementSetting: "true",
			},
			validate: func(t *testing.T, cluster *unstructured.Unstructured) {
				annotations := cluster.GetAnnotations()
				assert.Equal(t, "true", annotations["rancher.io/imported-cluster-version-management"])

				name, _, _ := unstructured.NestedString(cluster.Object, "name")
				assert.Equal(t, "test-cluster", name)

				desc, _, _ := unstructured.NestedString(cluster.Object, "description")
				assert.Equal(t, "A test cluster", desc)
			},
		},
		{
			name: "valid parameters with explicit version management false",
			params: createImportedClusterParams{
				Name:                     "my-cluster",
				VersionManagementSetting: "false",
			},
			validate: func(t *testing.T, cluster *unstructured.Unstructured) {
				annotations := cluster.GetAnnotations()
				assert.Equal(t, "false", annotations["rancher.io/imported-cluster-version-management"])
			},
		},
		{
			name: "empty version management defaults to system-default",
			params: createImportedClusterParams{
				Name:                     "my-cluster",
				VersionManagementSetting: "",
			},
			validate: func(t *testing.T, cluster *unstructured.Unstructured) {
				annotations := cluster.GetAnnotations()
				assert.Equal(t, "system-default", annotations["rancher.io/imported-cluster-version-management"])
			},
		},
		{
			name: "explicit system-default version management",
			params: createImportedClusterParams{
				Name:                     "my-cluster",
				VersionManagementSetting: "system-default",
			},
			validate: func(t *testing.T, cluster *unstructured.Unstructured) {
				annotations := cluster.GetAnnotations()
				assert.Equal(t, "system-default", annotations["rancher.io/imported-cluster-version-management"])
			},
		},
		{
			name: "missing name returns error",
			params: createImportedClusterParams{
				Name:                     "",
				Description:              "no name",
				VersionManagementSetting: "true",
			},
			expectedError: "name is required",
		},
		{
			name: "invalid version management setting returns error",
			params: createImportedClusterParams{
				Name:                     "my-cluster",
				VersionManagementSetting: "maybe",
			},
			expectedError: "invalid value for VersionManagementSetting: maybe",
		},
		{
			name: "description is set on the object",
			params: createImportedClusterParams{
				Name:                     "my-cluster",
				Description:              "hello world",
				VersionManagementSetting: "true",
			},
			validate: func(t *testing.T, cluster *unstructured.Unstructured) {
				desc, _, _ := unstructured.NestedString(cluster.Object, "description")
				assert.Equal(t, "hello world", desc)
			},
		},
		{
			name: "empty description is set on the object",
			params: createImportedClusterParams{
				Name:                     "my-cluster",
				Description:              "",
				VersionManagementSetting: "true",
			},
			validate: func(t *testing.T, cluster *unstructured.Unstructured) {
				desc, _, _ := unstructured.NestedString(cluster.Object, "description")
				assert.Equal(t, "", desc)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster, err := tools.createImportedClusterObj(test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
				assert.Nil(t, cluster)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cluster)
				if test.validate != nil {
					test.validate(t, cluster)
				}
			}
		})
	}
}
