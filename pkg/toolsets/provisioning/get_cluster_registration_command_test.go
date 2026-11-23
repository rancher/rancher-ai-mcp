package provisioning

import (
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	provisioningV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newProvCluster returns a provisioning cluster. Passing true as the optional last
// argument gives it an empty rkeConfig, which is what marks a cluster as custom.
func newProvCluster(name string, mgmtName string, machinePools bool, customRKE ...bool) *unstructured.Unstructured {
	spec := map[string]any{}
	if len(customRKE) > 0 && customRKE[0] {
		spec["rkeConfig"] = map[string]any{"etcd": map[string]any{}}
	}
	if machinePools {
		spec["rkeConfig"] = map[string]any{"machinePools": []any{map[string]any{"name": "pool"}}}
	}
	status := map[string]any{}
	if mgmtName != "" {
		status["clusterName"] = mgmtName
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "provisioning.cattle.io/v1",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": name, "namespace": "fleet-default"},
		"spec":       spec,
		"status":     status,
	}}
}

func newRegToken(name, ns, token string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "ClusterRegistrationToken",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"status": map[string]any{
			"token":       token,
			"nodeCommand": "sudo docker run rancher/rancher-agent --token " + token,
			"manifestUrl": "https://rancher/v3/import/" + token + ".yaml",
		},
	}}
}

func TestGetClusterRegistrationCommand(t *testing.T) {
	tests := map[string]struct {
		params        getClusterRegistrationCommandParams
		objects       []runtime.Object
		expectedText  []string
		expectedError string
	}{
		"returns commands": {
			params:       getClusterRegistrationCommandParams{Cluster: "custom"},
			objects:      []runtime.Object{newProvCluster("custom", "c-abcde", false), newRegToken("default-token", "c-abcde", "tok123")},
			expectedText: []string{"tok123", "nodeCommand", "manifestUrl"},
		},
		"token secret not found": {
			params: getClusterRegistrationCommandParams{Cluster: "custom"},
			objects: []runtime.Object{newProvCluster("custom", "c-abcde", false), &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "management.cattle.io/v3", "kind": "ClusterRegistrationToken",
				"metadata": map[string]any{"name": "default-token", "namespace": "c-abcde"},
				"status":   map[string]any{"token": "", "nodeCommand": "curl x --token {token}", "tokenSecretName": "crt-token-default-token"},
			}}},
			expectedError: `registration token secret "crt-token-default-token" was not found`,
		},
		"token interpolated from secret": {
			params: getClusterRegistrationCommandParams{Cluster: "custom"},
			objects: []runtime.Object{newProvCluster("custom", "c-abcde", false), &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "management.cattle.io/v3", "kind": "ClusterRegistrationToken",
				"metadata": map[string]any{"name": "default-token", "namespace": "c-abcde"},
				"status":   map[string]any{"nodeCommand": "curl x --token {token}", "tokenSecretName": "crt-token-default-token"},
			}}, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Secret",
				"metadata": map[string]any{"name": "crt-token-default-token", "namespace": "c-abcde"},
				"data":     map[string]any{"token": "c2VjcmV0dG9r"}, // secrettok
			}}},
			expectedText: []string{"--token secrettok"},
		},
		"resolves display name, imported": {
			params: getClusterRegistrationCommandParams{Cluster: "testing-cluster"},
			objects: []runtime.Object{&provisioningV1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "c-xyz", Namespace: "fleet-default", Annotations: map[string]string{"provisioning.cattle.io/management-cluster-display-name": "testing-cluster"}},
				Status:     provisioningV1.ClusterStatus{ClusterName: "c-xyz"},
			}, newMgmtCluster("c-xyz", "testing-cluster"), newRegToken("default-token", "c-xyz", "tok")},
			expectedText: []string{"c-xyz", `"clusterType":"imported"`},
		},
		"hosted cluster reports its type": {
			params: getClusterRegistrationCommandParams{Cluster: "hosted"},
			objects: []runtime.Object{newProvCluster("hosted", "c-host", false), &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "management.cattle.io/v3", "kind": "Cluster",
				"metadata": map[string]any{"name": "c-host"},
				"spec":     map[string]any{"eksConfig": map[string]any{"region": "eu-west-1"}},
			}}, newRegToken("default-token", "c-host", "tok")},
			expectedText: []string{`"clusterType":"hosted"`},
		},
		"token with only a command": {
			params: getClusterRegistrationCommandParams{Cluster: "custom"},
			objects: []runtime.Object{newProvCluster("custom", "c-abcde", false), &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "management.cattle.io/v3", "kind": "ClusterRegistrationToken",
				"metadata": map[string]any{"name": "default-token", "namespace": "c-abcde"},
				"status":   map[string]any{"command": "kubectl apply -f https://rancher/import.yaml"},
			}}},
			expectedText: []string{"kubectl apply"},
		},
		"display name annotation without matching management cluster": {
			params:        getClusterRegistrationCommandParams{Cluster: "testing-cluster"},
			objects:       []runtime.Object{displayNameCluster("c-fake"), newMgmtCluster("c-fake", "something-else")},
			expectedError: "not found",
		},
		"ambiguous display name": {
			params: getClusterRegistrationCommandParams{Cluster: "testing-cluster"},
			objects: []runtime.Object{displayNameCluster("c-one"), newMgmtCluster("c-one", "testing-cluster"),
				displayNameCluster("c-two"), newMgmtCluster("c-two", "testing-cluster")},
			expectedError: `matches several clusters in namespace "fleet-default" (c-one, c-two)`,
		},
		"cluster not found": {
			params:        getClusterRegistrationCommandParams{Cluster: "missing"},
			expectedError: `cluster "missing" not found in namespace "fleet-default" (checked cluster names and display names), use listClusters`,
		},
		"custom cluster reports its type": {
			params:       getClusterRegistrationCommandParams{Cluster: "custom"},
			objects:      []runtime.Object{newProvCluster("custom", "c-abcde", false, true), newRegToken("default-token", "c-abcde", "tok")},
			expectedText: []string{`"clusterType":"custom"`, "role flags"},
		},
		"display name that isn't a valid resource name": {
			params: getClusterRegistrationCommandParams{Cluster: "My Cluster"},
			objects: []runtime.Object{&provisioningV1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "c-xyz", Namespace: "fleet-default", Annotations: map[string]string{"provisioning.cattle.io/management-cluster-display-name": "My Cluster"}},
				Status:     provisioningV1.ClusterStatus{ClusterName: "c-xyz"},
			}, newMgmtCluster("c-xyz", "My Cluster"), newRegToken("default-token", "c-xyz", "tok")},
			expectedText: []string{"c-xyz", "tok"},
		},
		"empty cluster name": {
			params:        getClusterRegistrationCommandParams{},
			expectedError: "cluster name is required",
		},
		"no token": {
			params:       getClusterRegistrationCommandParams{Cluster: "custom"},
			objects:      []runtime.Object{newProvCluster("custom", "c-abcde", false)},
			expectedText: []string{"no registration token found"},
		},
		"machine pool cluster rejected": {
			params:        getClusterRegistrationCommandParams{Cluster: "pooled"},
			objects:       []runtime.Object{newProvCluster("pooled", "c-pool", true)},
			expectedError: "scaleClusterNodePool",
		},
		"local cluster rejected": {
			params:        getClusterRegistrationCommandParams{Cluster: "local"},
			expectedError: "local cluster",
		},
		"no management cluster yet": {
			params:        getClusterRegistrationCommandParams{Cluster: "custom"},
			objects:       []runtime.Object{newProvCluster("custom", "", false)},
			expectedError: "no management cluster",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			kinds := provisioningCustomListKinds()
			kinds[schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "clusterregistrationtokens"}] = "ClusterRegistrationTokenList"
			tools := newTestTools(dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), kinds, test.objects...))

			result, _, err := tools.getClusterRegistrationCommand(middleware.WithToken(t.Context(), testToken), newToolRequest("getClusterRegistrationCommand"), test.params)
			if test.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.expectedError)
				return
			}
			require.NoError(t, err)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			for _, s := range test.expectedText {
				assert.Contains(t, text.Text, s)
			}
		})
	}
}

func displayNameCluster(name string) *provisioningV1.Cluster {
	return &provisioningV1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "fleet-default", Annotations: map[string]string{"provisioning.cattle.io/management-cluster-display-name": "testing-cluster"}},
		Status:     provisioningV1.ClusterStatus{ClusterName: name},
	}
}

func newMgmtCluster(name, displayName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": name},
		"spec":       map[string]any{"displayName": displayName},
	}}
}

func TestGetClusterRegistrationCommandSecretForbidden(t *testing.T) {
	kinds := provisioningCustomListKinds()
	kinds[schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "clusterregistrationtokens"}] = "ClusterRegistrationTokenList"
	fakeDyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), kinds,
		newProvCluster("custom", "c-abcde", false),
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "management.cattle.io/v3", "kind": "ClusterRegistrationToken",
			"metadata": map[string]any{"name": "default-token", "namespace": "c-abcde"},
			"status":   map[string]any{"nodeCommand": "curl x --token {token}", "tokenSecretName": "crt-token-default-token"},
		}})
	fakeDyn.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "crt-token-default-token", fmt.Errorf("denied"))
	})

	_, _, err := newTestTools(fakeDyn).getClusterRegistrationCommand(middleware.WithToken(t.Context(), testToken), newToolRequest("getClusterRegistrationCommand"), getClusterRegistrationCommandParams{Cluster: "custom"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "you do not have permission to read the registration token secret")
	assert.True(t, apierrors.IsForbidden(err))
}

// When the caller can't read the token secret, Rancher can blank the commands, leaving only
// tokenSecretName. The tool must still try the secret read and report the permission error,
// and not claim that no registration token exists.
func TestGetClusterRegistrationCommandBlankedSecretBackedToken(t *testing.T) {
	kinds := provisioningCustomListKinds()
	kinds[schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "clusterregistrationtokens"}] = "ClusterRegistrationTokenList"
	fakeDyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), kinds,
		newProvCluster("custom", "c-abcde", false),
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "management.cattle.io/v3", "kind": "ClusterRegistrationToken",
			"metadata": map[string]any{"name": "default-token", "namespace": "c-abcde"},
			"status":   map[string]any{"tokenSecretName": "crt-token-default-token"},
		}})
	fakeDyn.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "crt-token-default-token", fmt.Errorf("denied"))
	})

	_, _, err := newTestTools(fakeDyn).getClusterRegistrationCommand(middleware.WithToken(t.Context(), testToken), newToolRequest("getClusterRegistrationCommand"), getClusterRegistrationCommandParams{Cluster: "custom"})
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err))
}
