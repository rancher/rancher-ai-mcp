package client

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

const (
	fakeUrl   = "https://localhost:8080"
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
		clusterIdsCache                      map[string]any
		clustersDisplayNameToIDCache         map[string]any
		expectedClusterIdsCache              map[string]any
		expectedClustersDisplayNameToIDCache map[string]any
		expectedID                           string
		expectErr                            string
	}{
		"should return clusterID if input is a clusterID": {
			clusterNameOrIDInput:                 clusterID,
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme(), newFakeCluster(clusterID, clusterDN)),
			expectedClusterIdsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{clusterDN: clusterID},
			expectedID:                           clusterID,
		},

		"should return clusterID if input is a cluster displayName": {
			clusterNameOrIDInput:                 clusterDN,
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme(), newFakeCluster(clusterID, clusterDN)),
			expectedClusterIdsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{clusterDN: clusterID},
			expectedID:                           clusterID,
		},

		"should return clusterID if clusterID is in the cache": {
			clusterNameOrIDInput:                 clusterID,
			clusterIdsCache:                      map[string]any{clusterID: struct{}{}},
			clustersDisplayNameToIDCache:         map[string]any{clusterDN: clusterID},
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme()),
			expectedClusterIdsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{clusterDN: clusterID},
			expectedID:                           clusterID,
		},

		"should return clusterID if displayName is in the cache": {
			clusterNameOrIDInput:                 clusterDN,
			clusterIdsCache:                      map[string]any{clusterID: struct{}{}},
			clustersDisplayNameToIDCache:         map[string]any{clusterDN: clusterID},
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme()),
			expectedClusterIdsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{clusterDN: clusterID},
			expectedID:                           clusterID,
		},

		"local": {
			clusterNameOrIDInput:                 "local",
			expectedClusterIdsCache:              map[string]any{},
			expectedClustersDisplayNameToIDCache: map[string]any{},
			expectedID:                           "local",
		},

		"cluster not found": {
			clusterNameOrIDInput:                 clusterDN,
			fakeDynClient:                        dynamicfake.NewSimpleDynamicClient(scheme(), newFakeCluster(clusterID, "another cluster")),
			expectedClusterIdsCache:              map[string]any{clusterID: struct{}{}},
			expectedClustersDisplayNameToIDCache: map[string]any{"another cluster": clusterID},
			expectErr:                            "cluster 'my-display-name' not found",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			clusterIdsCache = sync.Map{}
			if test.clusterIdsCache != nil {
				for key, value := range test.clusterIdsCache {
					clusterIdsCache.Store(key, value)
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

			clusterID, err := c.GetClusterID(context.TODO(), fakeToken, fakeUrl, test.clusterNameOrIDInput)

			if test.expectErr != "" {
				require.ErrorContains(t, err, test.expectErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, test.expectedID, clusterID)
			assert.Equal(t, test.expectedClusterIdsCache, syncMapToMap(&clusterIdsCache))
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
	_ = v1.AddToScheme(scheme)

	return scheme
}

func TestGetResource(t *testing.T) {
	fakePod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
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
				URL:       fakeUrl,
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
				URL:       fakeUrl,
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

			result, err := c.GetResource(context.Background(), test.params)

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
	fakePod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app": "nginx",
			},
		},
	}

	fakePod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app": "nginx",
			},
		},
	}

	fakePod3 := &v1.Pod{
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
				URL:       fakeUrl,
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
				URL:           fakeUrl,
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
				URL:       fakeUrl,
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

			results, err := c.GetResources(context.Background(), test.params)

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
