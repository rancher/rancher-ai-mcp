// Package auditlog provides tools for searching the Rancher API audit log.
package auditlog

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	toolsSet    = "rancher"
	toolsSetAnn = "toolset"
)

type toolsClient interface {
	CreateClientSet(ctx context.Context, token string, cluster string) (kubernetes.Interface, error)
}

// logOpener opens the log stream of a container in a pod.
type logOpener func(ctx context.Context, clientset kubernetes.Interface, namespace, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error)

// streamPodLogs opens a pod log stream through the Kubernetes API.
func streamPodLogs(ctx context.Context, clientset kubernetes.Interface, namespace, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
	return clientset.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
}

// Tools contains tools for reading the Rancher audit log.
type Tools struct {
	client   toolsClient
	openLogs logOpener
	pods     *podCache
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient) *Tools {
	return &Tools{
		client:   client,
		openLogs: streamPodLogs,
		pods:     newPodCache(podCacheTTL),
	}
}

// AddTools registers the audit log tools with the provided MCP server.
func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "searchAuditLogs",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Searches the Rancher API audit log to find who did what and when, for example who created, changed or deleted a user, role binding, cluster or other resource. Entries are returned newest first.
Rancher must be installed with auditLog.enabled=true and auditLog.destination=sidecar, and only entries still in the current container log are available.
Resources appear in request URIs and bodies by ID (e.g. u-abc12 for users), so look up the ID first (e.g. with getUser) and search for both the ID and the name.
To find changes rather than reads, use changesOnly.`},
		t.searchAuditLogs,
	)
}
