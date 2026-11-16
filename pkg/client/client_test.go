package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

const (
	fakeURL   = "https://localhost:8080"
	fakeToken = "token-xxx"
)

// helper to create a fake cluster object for tests.
func newFakeCluster(id, displayName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "management.cattle.io/v3",
			"kind":       "Cluster",
			"metadata": map[string]any{
				"name": id,
			},
			"spec": map[string]any{
				"displayName": displayName,
			},
		},
	}
}

func TestGetClusterId(t *testing.T) {
	const (
		clusterID = "c-m-12345"
		clusterDN = "my-display-name"
	)

	tests := map[string]struct {
		clusterNameOrIDInput                 string
		fakeDynClient                        *dynamicfake.FakeDynamicClient
		clusterIDsCache                      map[string]any
		clustersDisplayNameToIDCache         map[string]any
		expectedClusterIDsCache              map[string]any
		expectedClustersDisplayNameToIDCache map[string]any
		expectedID                           string
		expectErr                            string
	}{
		"should return clusterID if input is a clusterID": {
			clusterNameOrIDInput:                 clusterID,
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme(), newFakeCluster(clusterID, clusterDN)),
			expectedClusterIDsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{clusterDN: clusterID},
			expectedID:                           clusterID,
		},

		"should return clusterID if input is a cluster displayName": {
			clusterNameOrIDInput:                 clusterDN,
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme(), newFakeCluster(clusterID, clusterDN)),
			expectedClusterIDsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{clusterDN: clusterID},
			expectedID:                           clusterID,
		},

		"should return clusterID if clusterID is in the cache": {
			clusterNameOrIDInput:                 clusterID,
			clusterIDsCache:                      map[string]any{clusterID: struct{}{}},
			clustersDisplayNameToIDCache:         map[string]any{clusterDN: clusterID},
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme()),
			expectedClusterIDsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{clusterDN: clusterID},
			expectedID:                           clusterID,
		},

		"should return clusterID if displayName is in the cache": {
			clusterNameOrIDInput:                 clusterDN,
			clusterIDsCache:                      map[string]any{clusterID: struct{}{}},
			clustersDisplayNameToIDCache:         map[string]any{clusterDN: clusterID},
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme()),
			expectedClusterIDsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{clusterDN: clusterID},
			expectedID:                           clusterID,
		},

		"local": {
			clusterNameOrIDInput:                 "local",
			expectedClusterIDsCache:              map[string]any{},
			expectedClustersDisplayNameToIDCache: map[string]any{},
			expectedID:                           "local",
		},

		"cluster not found": {
			clusterNameOrIDInput:                 clusterDN,
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme(), newFakeCluster(clusterID, "another cluster")),
			expectedClusterIDsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{"another cluster": clusterID},
			expectErr:                            "cluster 'my-display-name' not found",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			clusterIDsCache = sync.Map{}
			if test.clusterIDsCache != nil {
				for key, value := range test.clusterIDsCache {
					clusterIDsCache.Store(key, value)
				}
			}

			clustersDisplayNameToIDCache = sync.Map{}
			if test.clustersDisplayNameToIDCache != nil {
				for key, value := range test.clustersDisplayNameToIDCache {
					clustersDisplayNameToIDCache.Store(key, value)
				}
			}

			c := &Client{
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return test.fakeDynClient, nil
				},
			}

			clusterID, err := c.GetClusterID(t.Context(), fakeToken, test.clusterNameOrIDInput)

			if test.expectErr != "" {
				require.ErrorContains(t, err, test.expectErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, test.expectedID, clusterID)
			assert.Equal(t, test.expectedClusterIDsCache, syncMapToMap(&clusterIDsCache))
			assert.Equal(t, test.expectedClustersDisplayNameToIDCache, syncMapToMap(&clustersDisplayNameToIDCache))
		})
	}
}

func syncMapToMap(syncMap *sync.Map) map[string]any {
	result := make(map[string]any)

	syncMap.Range(func(key, value any) bool {
		result[key.(string)] = value
		return true
	})

	return result
}

func scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	return scheme
}

func TestGetResource(t *testing.T) {
	fakePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "nginx",
					Image: "nginx:latest",
				},
			},
		},
	}

	tests := map[string]struct {
		params        GetParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		expectedName  string
		expectedError string
	}{
		"get pod successfully": {
			params: GetParams{
				Cluster:   "local",
				Kind:      "pod",
				Namespace: "default",
				Name:      "test-pod",
				Token:     fakeToken,
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(scheme(), fakePod),
			expectedName:  "test-pod",
		},
		"resource not found": {
			params: GetParams{
				Cluster:   "local",
				Kind:      "pod",
				Namespace: "default",
				Name:      "nonexistent-pod",
				Token:     fakeToken,
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(scheme()),
			expectedError: `pods "nonexistent-pod" not found`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			c := &Client{
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return test.fakeDynClient, nil
				},
			}

			result, err := c.GetResource(t.Context(), test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, test.expectedName, result.GetName())
			}
		})
	}
}

func TestGetResources(t *testing.T) {
	fakePod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app": "nginx",
			},
		},
	}

	fakePod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app": "nginx",
			},
		},
	}

	fakePod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-3",
			Namespace: "default",
			Labels: map[string]string{
				"app": "redis",
			},
		},
	}

	tests := map[string]struct {
		params        ListParams
		fakeDynClient *dynamicfake.FakeDynamicClient
		expectedCount int
		expectedNames []string
		expectedError string
	}{
		"list all pods in namespace": {
			params: ListParams{
				Cluster:   "local",
				Kind:      "pod",
				Namespace: "default",
				Token:     fakeToken,
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(scheme(), fakePod1, fakePod2, fakePod3),
			expectedCount: 3,
			expectedNames: []string{"pod-1", "pod-2", "pod-3"},
		},
		"list pods with label selector": {
			params: ListParams{
				Cluster:       "local",
				Kind:          "pod",
				Namespace:     "default",
				Token:         fakeToken,
				LabelSelector: "app=nginx",
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(scheme(), fakePod1, fakePod2, fakePod3),
			expectedCount: 2,
			expectedNames: []string{"pod-1", "pod-2"},
		},
		"list empty namespace": {
			params: ListParams{
				Cluster:   "local",
				Kind:      "pod",
				Namespace: "kube-system",
				Token:     fakeToken,
			},
			fakeDynClient: dynamicfake.NewSimpleDynamicClient(scheme()),
			expectedCount: 0,
			expectedNames: []string{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			c := &Client{
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return test.fakeDynClient, nil
				},
			}

			results, err := c.GetResources(t.Context(), test.params)

			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
				assert.Nil(t, results)
			} else {
				assert.NoError(t, err)
				assert.Len(t, results, test.expectedCount)

				actualNames := make([]string, len(results))
				for i, result := range results {
					actualNames[i] = result.GetName()
				}

				assert.ElementsMatch(t, test.expectedNames, actualNames)
			}
		})
	}
}

func TestRancherURLFromAuthServerURL(t *testing.T) {
	testCases := map[string]struct {
		input    string
		expected string
		wantErr  bool
	}{
		"empty input returns empty string": {
			input:    "",
			expected: "",
			wantErr:  false,
		},
		"removes path query and fragment": {
			input:    "https://rancher.example.com/v3-public/auth?scope=openid#section",
			expected: "https://rancher.example.com",
			wantErr:  false,
		},
		"keeps scheme host and port": {
			input:    "https://rancher.example.com:9443/oauth2/authorize",
			expected: "https://rancher.example.com:9443",
			wantErr:  false,
		},
		"strips trailing slash-only path": {
			input:    "https://rancher.example.com/",
			expected: "https://rancher.example.com",
			wantErr:  false,
		},
		"invalid URL returns error": {
			input:   "http://[::1",
			wantErr: true,
		},
	}

	for name, tt := range testCases {
		t.Run(name, func(t *testing.T) {
			actual, err := rancherURLFromAuthServerURL(tt.input)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestCreateRestConfig(t *testing.T) {
	tests := map[string]struct {
		client             *Client
		token              string
		clusterID          string
		expectInsecure     bool
		expectCAData       []byte
		expectServerSuffix string
	}{
		"insecure mode sets InsecureSkipTLSVerify": {
			client: &Client{
				insecure:   true,
				rancherURL: fakeURL,
			},
			token:              fakeToken,
			clusterID:          "local",
			expectInsecure:     true,
			expectCAData:       nil,
			expectServerSuffix: "/k8s/clusters/local",
		},
		"secure mode with caBundle sets CAData": {
			client: &Client{
				insecure:   false,
				rancherURL: fakeURL,
				caBundle:   []byte("-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----"),
			},
			token:              fakeToken,
			clusterID:          "c-m-12345",
			expectInsecure:     false,
			expectCAData:       []byte("-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----"),
			expectServerSuffix: "/k8s/clusters/c-m-12345",
		},
		"secure mode without caBundle leaves both empty": {
			client: &Client{
				insecure:   false,
				rancherURL: fakeURL,
				caBundle:   nil,
			},
			token:              fakeToken,
			clusterID:          "local",
			expectInsecure:     false,
			expectCAData:       nil,
			expectServerSuffix: "/k8s/clusters/local",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			cfg, err := test.client.CreateRestConfig(test.token, test.clusterID)
			require.NoError(t, err)

			assert.Contains(t, cfg.Host, test.expectServerSuffix)
			assert.Equal(t, "Bearer "+fakeToken, "Bearer "+cfg.BearerToken)
			assert.Equal(t, test.expectInsecure, cfg.Insecure)
			assert.Equal(t, test.expectCAData, cfg.CAData)
		})
	}
}

func TestNewClient_RancherURL(t *testing.T) {
	tests := map[string]struct {
		authzServerURL string
		envVar         string
		expectedURL    string
	}{
		"uses authzServerURL when provided": {
			authzServerURL: "https://rancher.example.com/v3-public/auth",
			expectedURL:    "https://rancher.example.com",
		},
		"falls back to RANCHER_URL env var": {
			authzServerURL: "",
			envVar:         "https://my-rancher.internal",
			expectedURL:    "https://my-rancher.internal",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if test.envVar != "" {
				t.Setenv("RANCHER_URL", test.envVar)
			} else {
				require.NoError(t, os.Unsetenv("RANCHER_URL"))
			}

			c, err := NewClient(true, test.authzServerURL)
			require.NoError(t, err)
			assert.Equal(t, test.expectedURL, c.RancherURL())
		})
	}
}

func TestNewClientWithURL(t *testing.T) {
	tests := map[string]struct {
		rancherURL  string
		expectedURL string
		expectedErr string
	}{
		"uses the provided URL": {
			rancherURL:  "https://rancher.example.com",
			expectedURL: "https://rancher.example.com",
		},
		"trims trailing slash": {
			rancherURL:  "https://rancher.example.com/",
			expectedURL: "https://rancher.example.com",
		},
		"fails with empty URL": {
			rancherURL:  "",
			expectedErr: "rancher URL is required",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// insecure=false must not fetch the CA bundle from the in-cluster config
			c, err := NewClientWithURL(false, test.rancherURL)
			if test.expectedErr != "" {
				assert.ErrorContains(t, err, test.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.expectedURL, c.RancherURL())
			assert.Empty(t, c.caBundle)
		})
	}
}

func TestGetRancherURL(t *testing.T) {
	type collection struct {
		Kind string `json:"kind"`
		Data []any  `json:"data"`
	}

	tests := map[string]struct {
		method         string
		path           string
		responseStatus int
		responseBody   string
		out            any
		expectedOut    any
		expectedErr    string
		// errCheck, if set, must return true for the returned error.
		errCheck func(error) bool
	}{
		"GET returns raw body on 200 with nil out": {
			method:         http.MethodGet,
			path:           "/v1/management.cattle.io.clusters",
			responseStatus: http.StatusOK,
			responseBody:   `{"kind":"Collection","data":[]}`,
		},
		"GET unmarshals into out when non-nil": {
			method:         http.MethodGet,
			path:           "/v1/management.cattle.io.clusters",
			responseStatus: http.StatusOK,
			responseBody:   `{"kind":"Collection","data":[]}`,
			out:            &collection{},
			expectedOut:    &collection{Kind: "Collection", Data: []any{}},
		},
		"DELETE returns body on 200": {
			method:         http.MethodDelete,
			path:           "/v1/management.cattle.io.clusters/c-m-12345",
			responseStatus: http.StatusOK,
			responseBody:   `{}`,
		},
		"POST accepts 201 and unmarshals into out": {
			method:         http.MethodPost,
			path:           "/v1/management.cattle.io.clusters",
			responseStatus: http.StatusCreated,
			responseBody:   `{"kind":"Collection","data":[]}`,
			out:            &collection{},
			expectedOut:    &collection{Kind: "Collection", Data: []any{}},
		},
		"DELETE accepts 204 without unmarshalling into out": {
			method:         http.MethodDelete,
			path:           "/v1/management.cattle.io.clusters/c-m-12345",
			responseStatus: http.StatusNoContent,
			out:            &collection{},
			expectedOut:    &collection{},
		},
		"returns Unauthorized error on 401 with Rancher error body": {
			method:         http.MethodGet,
			path:           "/v3/users",
			responseStatus: http.StatusUnauthorized,
			responseBody:   `{"type":"error","status":"401","message":"Unauthorized 401: must authenticate"}`,
			expectedErr:    "/v3/users failed: the server has asked for the client to provide credentials",
			errCheck:       apierrors.IsUnauthorized,
		},
		"returns Forbidden error with server message on 403": {
			method:         http.MethodGet,
			path:           "/v1/management.cattle.io.users",
			responseStatus: http.StatusForbidden,
			responseBody:   `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"users.management.cattle.io is forbidden: User \"u-abc\" cannot list resource \"users\"","reason":"Forbidden","code":403}`,
			expectedErr:    `User "u-abc" cannot list resource "users"`,
			errCheck:       apierrors.IsForbidden,
		},
		"returns NotFound error on 404": {
			method:         http.MethodGet,
			path:           "/v1/management.cattle.io.users/missing",
			responseStatus: http.StatusNotFound,
			responseBody:   `404 page not found`,
			expectedErr:    "/v1/management.cattle.io.users/missing failed",
			errCheck:       apierrors.IsNotFound,
		},
		"returns InternalError with plain text body on 500": {
			method:         http.MethodGet,
			path:           "/v1/management.cattle.io.clusters",
			responseStatus: http.StatusInternalServerError,
			responseBody:   "something broke\n",
			expectedErr:    `an error on the server ("something broke")`,
			errCheck:       apierrors.IsInternalError,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var (
				receivedAuth   string
				receivedMethod string
				receivedPath   string
			)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedAuth = r.Header.Get("Authorization")
				receivedMethod = r.Method
				receivedPath = r.URL.Path

				w.WriteHeader(test.responseStatus)
				_, _ = fmt.Fprint(w, test.responseBody)
			}))
			t.Cleanup(server.Close)

			c := &Client{insecure: true, rancherURL: server.URL}

			err := c.GetRancherURL(t.Context(), fakeToken, test.method, test.path, nil, test.out)

			assert.Equal(t, test.method, receivedMethod)
			assert.Equal(t, test.path, receivedPath)
			assert.Equal(t, "Bearer "+fakeToken, receivedAuth)

			if test.expectedErr != "" {
				require.ErrorContains(t, err, test.expectedErr)

				if test.errCheck != nil {
					assert.True(t, test.errCheck(err), "unexpected error type: %v", err)
				}

				return
			}

			require.NoError(t, err)

			if test.expectedOut != nil {
				assert.Equal(t, test.expectedOut, test.out)
			}
		})
	}
}

func TestGetRancherURLWithoutToken(t *testing.T) {
	var (
		hasAuth bool
		called  bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, hasAuth = r.Header["Authorization"]

		_, _ = fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(server.Close)

	c := &Client{insecure: true, rancherURL: server.URL}

	require.NoError(t, c.GetRancherURL(t.Context(), "", http.MethodGet, "/v3-public/authProviders", nil, nil))
	assert.True(t, called)
	assert.False(t, hasAuth, "an empty token should not send an Authorization header")
}
