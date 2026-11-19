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
}

// NewTools creates and returns a new Tools instance.
func NewTools(client toolsClient) *Tools {
	return &Tools{
		client:   client,
		openLogs: streamPodLogs,
	}
}

// AddTools registers the audit log tools with the provided MCP server.
func (t *Tools) AddTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "searchAuditLogs",
		Meta: map[string]any{
			toolsSetAnn: toolsSet,
		},
		Description: `Searches the Rancher API audit log to find who did what and when, for example who created, changed or deleted a user, role binding, cluster or other resource.
Entries are returned newest first with the user ID, username, auth provider and principal ID of the user that made the request, the real user that impersonated it (impersonatedBy) if any, the login name of login requests, HTTP method, request URI, response code, timestamps and a request body truncated to 512 bytes. Set includeBodies to get request and response bodies of up to 8192 bytes each.
All filters are optional and are combined with AND. By default all requests are returned, including reads (GET) and read-only POSTs. To find changes rather than reads, set changesOnly to true: only then are requests limited to POST, PUT, PATCH and DELETE, leaving out read-only POSTs such as the UI's constant who-am-I calls to /v1/ext.cattle.io.selfuser and Kubernetes access and token reviews, and the response says how many were left out. Setting method to "POST,PUT,PATCH,DELETE" instead also returns those read-only POSTs.
To leave out other noisy requests, set excludeRequestURI to comma-separated substrings of the request URI (e.g. "ext.cattle.io.selfuser,/v1/userpreferences").
Resources are often referred to by ID in request URIs and bodies (e.g. u-abc12 for users), so look up the resource ID first (e.g. with getUser) and search for both the ID and the name.
The audit log is read from the rancher-audit-log sidecar container of the Rancher pods, so Rancher must be installed with auditLog.enabled=true and auditLog.destination=sidecar. Only entries still in the current container log are available.

Results are paginated with limit (page size, default 50) and offset (how many matching entries to skip, default 0). When more entries remain, the response includes the exact offset value to pass in for the next page, and an until value that limits shifting of the pages while new requests are logged (timestamps have one-second precision, so requests logged in the same second may still shift them). Each call reads the log again.`},
		t.searchAuditLogs,
	)
}
