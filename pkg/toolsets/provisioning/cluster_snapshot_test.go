package provisioning

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func newSnapshotTestTools(objs ...runtime.Object) Tools {
	tools, _ := newSnapshotTestToolsWithClient(objs...)
	return tools
}

func newSnapshotTestToolsWithClient(objs ...runtime.Object) (Tools, *dynamicfake.FakeDynamicClient) {
	listKinds := provisioningCustomListKinds()
	listKinds[schema.GroupVersionResource{Group: "rke.cattle.io", Version: "v1", Resource: "etcdsnapshots"}] = "ETCDSnapshotList"
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(provisioningSchemes(), listKinds, objs...)

	return Tools{client: &client.Client{
		ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
			return newFakeClientSet(), nil
		},
		DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
			return dynClient, nil
		},
	}}, dynClient
}

func newProvisioningClusterWithSnapshotGeneration(name, namespace string, generation *int) *unstructured.Unstructured {
	cluster := newProvisioningClusterWithRKEConfig(name, namespace, "c-m-abc123", nil)
	// the fake client does not set resourceVersion, which the snapshot patch tests.
	cluster.SetResourceVersion("1")
	if generation != nil {
		_ = unstructured.SetNestedField(cluster.Object, int64(*generation), "spec", "rkeConfig", "etcdSnapshotCreate", "generation")
	}
	return cluster
}

func newETCDSnapshot(name, namespace, clusterName string, createdAt time.Time, s3 bool) *unstructured.Unstructured {
	snapshot := &rkev1.ETCDSnapshot{
		TypeMeta: metav1.TypeMeta{APIVersion: "rke.cattle.io/v1", Kind: "ETCDSnapshot"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{snapshotClusterNameLabel: clusterName},
		},
		Spec: rkev1.ETCDSnapshotSpec{ClusterName: clusterName},
		SnapshotFile: rkev1.ETCDSnapshotFile{
			Name:      name,
			NodeName:  clusterName + "-node-1",
			Location:  "file:///var/lib/rancher/rke2/server/db/snapshots/" + name,
			Metadata:  "H4sIAAAAAAAA/large-gzipped-spec",
			CreatedAt: &metav1.Time{Time: createdAt},
			Size:      1024,
			Status:    "successful",
		},
	}
	if s3 {
		snapshot.SnapshotFile.Location = "s3://bucket/" + name
		snapshot.SnapshotFile.S3 = &rkev1.ETCDSnapshotS3{Bucket: "bucket"}
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(snapshot)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func callToolRequest(name string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: name}}
}

func TestCreateClusterSnapshot(t *testing.T) {
	tests := []struct {
		name               string
		objs               []runtime.Object
		params             createClusterSnapshotParams
		expectedGeneration int64
		expectedError      string
	}{
		{
			name:               "first snapshot sets generation to 1",
			objs:               []runtime.Object{newProvisioningClusterWithSnapshotGeneration("prod", "fleet-default", nil)},
			params:             createClusterSnapshotParams{Cluster: "prod"},
			expectedGeneration: 1,
		},
		{
			name:               "existing generation is incremented",
			objs:               []runtime.Object{newProvisioningClusterWithSnapshotGeneration("prod", "fleet-default", ptr.To(4))},
			params:             createClusterSnapshotParams{Cluster: "prod", Namespace: "fleet-default"},
			expectedGeneration: 5,
		},
		{
			name:          "imported cluster without rkeConfig is rejected",
			objs:          []runtime.Object{newProvisioningCluster("imported", "fleet-default", "c-m-abc123")},
			params:        createClusterSnapshotParams{Cluster: "imported"},
			expectedError: "cluster imported is not an rke2 or k3s cluster provisioned by Rancher",
		},
		{
			name:          "local cluster is rejected",
			objs:          []runtime.Object{newProvisioningCluster("local", "fleet-local", "local")},
			params:        createClusterSnapshotParams{Cluster: "local"},
			expectedError: "taking etcd snapshots of the local cluster is not supported",
		},
		{
			name:          "missing cluster",
			params:        createClusterSnapshotParams{Cluster: "missing"},
			expectedError: `cluster.provisioning.cattle.io "missing" not found`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tools := newSnapshotTestTools(test.objs...)

			result, _, err := tools.createClusterSnapshot(context.Background(), callToolRequest("createClusterSnapshot"), test.params)
			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
				return
			}
			require.NoError(t, err)

			text := result.Content[0].(*mcp.TextContent).Text
			assert.Contains(t, text, createSnapshotNote)

			cluster, err := tools.client.GetResource(context.Background(), client.GetParams{
				Cluster:   LocalCluster,
				Kind:      "provisioningcluster",
				Namespace: "fleet-default",
				Name:      test.params.Cluster,
			})
			require.NoError(t, err)
			generation, _, _ := unstructured.NestedInt64(cluster.Object, "spec", "rkeConfig", "etcdSnapshotCreate", "generation")
			assert.Equal(t, test.expectedGeneration, generation)
		})
	}
}

func TestCreateClusterSnapshotPlan(t *testing.T) {
	tools := newSnapshotTestTools(newProvisioningClusterWithSnapshotGeneration("prod", "fleet-default", ptr.To(2)))

	result, _, err := tools.createClusterSnapshotPlan(context.Background(), callToolRequest("createClusterSnapshotPlan"), createClusterSnapshotParams{Cluster: "prod"})
	require.NoError(t, err)

	assert.JSONEq(t, `[
  {
    "type": "update",
    "payload": [{"op": "test", "path": "/metadata/resourceVersion", "value": "1"}, {"op": "add", "path": "/spec/rkeConfig/etcdSnapshotCreate", "value": {"generation": 3}}],
    "resource": {"name": "prod", "kind": "provisioningcluster", "cluster": "local", "namespace": "fleet-default"}
  }
]`, result.Content[0].(*mcp.TextContent).Text)

	// the plan must not modify the cluster
	cluster, err := tools.client.GetResource(context.Background(), client.GetParams{
		Cluster: LocalCluster, Kind: "provisioningcluster", Namespace: "fleet-default", Name: "prod",
	})
	require.NoError(t, err)
	generation, _, _ := unstructured.NestedInt64(cluster.Object, "spec", "rkeConfig", "etcdSnapshotCreate", "generation")
	assert.Equal(t, int64(2), generation)
}

func TestCreateClusterSnapshotConcurrentModification(t *testing.T) {
	tools, dynClient := newSnapshotTestToolsWithClient(newProvisioningClusterWithSnapshotGeneration("prod", "fleet-default", ptr.To(2)))

	// Return a stale copy of the cluster from the read, as if another client
	// requested a snapshot between the read and the patch.
	dynClient.PrependReactor("get", "clusters", func(action k8stesting.Action) (bool, runtime.Object, error) {
		stale := newProvisioningClusterWithSnapshotGeneration("prod", "fleet-default", ptr.To(1))
		stale.SetResourceVersion("0")
		return true, stale, nil
	})

	_, _, err := tools.createClusterSnapshot(context.Background(), callToolRequest("createClusterSnapshot"), createClusterSnapshotParams{Cluster: "prod"})
	require.Error(t, err)

	// the stale generation must not have been written over the newer one
	cluster, err := dynClient.Tracker().Get(schema.GroupVersionResource{Group: "provisioning.cattle.io", Version: "v1", Resource: "clusters"}, "fleet-default", "prod")
	require.NoError(t, err)
	generation, _, _ := unstructured.NestedInt64(cluster.(*unstructured.Unstructured).Object, "spec", "rkeConfig", "etcdSnapshotCreate", "generation")
	assert.Equal(t, int64(2), generation)
}

func TestCreateClusterSnapshotPlanRejectsLocalCluster(t *testing.T) {
	tools := newSnapshotTestTools(newProvisioningCluster("local", "fleet-local", "local"))

	_, _, err := tools.createClusterSnapshotPlan(context.Background(), callToolRequest("createClusterSnapshotPlan"), createClusterSnapshotParams{Cluster: "local"})
	assert.ErrorContains(t, err, "taking etcd snapshots of the local cluster is not supported")
}

func TestListClusterSnapshots(t *testing.T) {
	older := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		objs           []runtime.Object
		params         listClusterSnapshotsParams
		expectedResult string
		expectedError  string
	}{
		{
			name: "snapshots for the cluster are returned newest first",
			objs: []runtime.Object{
				newETCDSnapshot("prod-snap-old", "fleet-default", "prod", older, false),
				newETCDSnapshot("prod-snap-new", "fleet-default", "prod", newer, true),
				newETCDSnapshot("dev-snap", "fleet-default", "dev", newer, false),
			},
			params: listClusterSnapshotsParams{Cluster: "prod"},
			expectedResult: `{
  "llm": [
    {
      "name": "prod-snap-new",
      "createdAt": "2026-10-02T12:00:00Z",
      "nodeName": "prod-node-1",
      "location": "s3://bucket/prod-snap-new",
      "storage": "s3",
      "sizeBytes": 1024,
      "status": "successful"
    },
    {
      "name": "prod-snap-old",
      "createdAt": "2026-10-01T12:00:00Z",
      "nodeName": "prod-node-1",
      "location": "file:///var/lib/rancher/rke2/server/db/snapshots/prod-snap-old",
      "storage": "local",
      "sizeBytes": 1024,
      "status": "successful"
    }
  ],
  "uiContext": [
    {"namespace": "fleet-default", "kind": "ETCDSnapshot", "cluster": "local", "name": "prod-snap-new", "type": "rke.cattle.io.etcdsnapshot"},
    {"namespace": "fleet-default", "kind": "ETCDSnapshot", "cluster": "local", "name": "prod-snap-old", "type": "rke.cattle.io.etcdsnapshot"}
  ]
}`,
		},
		{
			name:           "no snapshots",
			objs:           []runtime.Object{newETCDSnapshot("dev-snap", "fleet-default", "dev", newer, false)},
			params:         listClusterSnapshotsParams{Cluster: "prod"},
			expectedResult: `{"llm": "no snapshots found"}`,
		},
		{
			name:          "invalid cluster name",
			params:        listClusterSnapshotsParams{Cluster: "Not_Valid"},
			expectedError: "cluster names may only contain lowercase alphanumeric characters",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tools := newSnapshotTestTools(test.objs...)

			result, _, err := tools.listClusterSnapshots(context.Background(), callToolRequest("listClusterSnapshots"), test.params)
			if test.expectedError != "" {
				assert.ErrorContains(t, err, test.expectedError)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, test.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
