package provisioning

import (
	"context"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type listClusterSnapshotsParams struct {
	Cluster   string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace of the provisioning cluster. Defaults to fleet-default, or fleet-local for the local cluster"`
}

// snapshotSummary contains the key details of an etcd snapshot. The gzipped
// cluster spec metadata is intentionally omitted as it is large and not
// useful to the LLM.
type snapshotSummary struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt,omitempty"`
	NodeName  string `json:"nodeName,omitempty"`
	Location  string `json:"location,omitempty"`
	Storage   string `json:"storage"`
	SizeBytes int64  `json:"sizeBytes"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (params *listClusterSnapshotsParams) validate(toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
	params.Namespace = clusterResourcesNamespace(params.Cluster, params.Namespace)

	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":   params.Cluster,
		"namespace": params.Namespace,
	})

	if err := validateClusterName(params.Cluster); err != nil {
		return nil, err
	}

	return log, nil
}

// snapshotClusterNameLabel is the label Rancher sets on each etcd snapshot
// with the name of the cluster it belongs to.
const snapshotClusterNameLabel = "rke.cattle.io/cluster-name"

// listClusterSnapshots returns a summary of all etcd snapshots for a cluster, newest first.
func (t *Tools) listClusterSnapshots(ctx context.Context, toolReq *mcp.CallToolRequest, params listClusterSnapshotsParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Listing cluster etcd snapshots")

	objs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       LocalCluster,
		Kind:          "etcdsnapshot",
		Namespace:     params.Namespace,
		Token:         middleware.Token(ctx),
		LabelSelector: snapshotClusterNameLabel + "=" + params.Cluster,
	})
	if err != nil {
		log.Error("failed to list etcd snapshots", zap.Error(err))
		return nil, nil, err
	}

	var snapshots []rkev1.ETCDSnapshot
	for _, obj := range objs {
		var snapshot rkev1.ETCDSnapshot
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &snapshot); err != nil {
			log.Error("failed to convert etcd snapshot from unstructured", zap.String("name", obj.GetName()), zap.Error(err))
			return nil, nil, err
		}
		snapshots = append(snapshots, snapshot)
	}

	sort.SliceStable(snapshots, func(i, j int) bool {
		return snapshotCreatedAt(snapshots[i]).After(snapshotCreatedAt(snapshots[j]).Time)
	})

	summaries := make([]snapshotSummary, 0, len(snapshots))
	uiContext := make([]response.UIContext, 0, len(snapshots))
	for _, snapshot := range snapshots {
		summary := snapshotSummary{
			Name:      snapshot.Name,
			NodeName:  snapshot.SnapshotFile.NodeName,
			Location:  snapshot.SnapshotFile.Location,
			Storage:   "local",
			SizeBytes: snapshot.SnapshotFile.Size,
			Status:    snapshot.SnapshotFile.Status,
			Message:   snapshot.SnapshotFile.Message,
		}
		if snapshot.SnapshotFile.S3 != nil {
			summary.Storage = "s3"
		}
		if createdAt := snapshotCreatedAt(snapshot); !createdAt.IsZero() {
			summary.CreatedAt = createdAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		summaries = append(summaries, summary)

		uiContext = append(uiContext, response.UIContext{
			Namespace: snapshot.Namespace,
			Kind:      "ETCDSnapshot",
			Cluster:   LocalCluster,
			Name:      snapshot.Name,
			Type:      converter.RKEGroup + ".etcdsnapshot",
		})
	}

	log.Debug("Found cluster etcd snapshots", zap.Int("count", len(summaries)))

	var data any = summaries
	if len(summaries) == 0 {
		data = "no snapshots found"
	}

	mcpResponse, err := response.CreateMcpResponseAny(data, uiContext...)
	if err != nil {
		log.Error("failed to create mcp response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// snapshotCreatedAt returns the time the snapshot file was created, falling
// back to the creation time of the resource if it is not set.
func snapshotCreatedAt(snapshot rkev1.ETCDSnapshot) metav1.Time {
	if snapshot.SnapshotFile.CreatedAt != nil {
		return *snapshot.SnapshotFile.CreatedAt
	}
	return snapshot.CreationTimestamp
}
