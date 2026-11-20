package auditlog

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	localCluster = "local"
	// rancherNamespace is the namespace of the Rancher server pods. They are recognised by their audit log
	// sidecar container rather than by a label, because the labels of the Rancher chart depend on the name of
	// the Helm release.
	rancherNamespace = "cattle-system"
	// auditLogContainer is the sidecar container of the Rancher pods that writes the audit log to its output
	// when Rancher is installed with auditLog.destination=sidecar.
	auditLogContainer = "rancher-audit-log"

	defaultLimit = 50
	maxLimit     = 500
	// maxWindow is the maximum of offset+limit. Only the newest offset+limit matching entries are kept in
	// memory while the log is read, so this bounds the memory used by a search.
	maxWindow = 5000
	// sinceMargin is subtracted from the since filter when requesting logs from the Kubernetes API. The API
	// filters on the time the kubelet received the line, which is always after the request timestamp written
	// by Rancher, so the margin only allows for clock differences between Rancher and the kubelet.
	sinceMargin = time.Minute
)

// searchAuditLogsParams specifies the parameters for searching the Rancher audit log.
type searchAuditLogsParams struct {
	User              string `json:"user,omitempty" jsonschema:"optional user to filter by. Matches the Rancher user ID (e.g. user-abc12 or u-abc12), the username (e.g. admin or an external login such as jane@example.com) or the principal ID (e.g. keycloakoidc_user://1234) of the user that made the request or of the real user that impersonated it, or the login name of a login request, case-insensitively"`
	Method            string `json:"method,omitempty" jsonschema:"optional comma-separated list of HTTP methods to filter by (e.g. DELETE or POST,PUT,PATCH,DELETE to find changes)"`
	RequestURI        string `json:"requestURI,omitempty" jsonschema:"optional case-insensitive substring of the request URI to filter by (e.g. /v3/globalrolebindings or management.cattle.io.users)"`
	ExcludeRequestURI string `json:"excludeRequestURI,omitempty" jsonschema:"optional comma-separated list of case-insensitive substrings, entries whose request URI contains any of them are left out (e.g. ext.cattle.io.selfuser to leave out the constant read-only who-am-I requests of the UI)"`
	ChangesOnly       bool   `json:"changesOnly,omitempty" jsonschema:"set to true to return only requests that change something, i.e. POST, PUT, PATCH and DELETE requests, leaving out read-only requests such as the UI's constant who-am-I POSTs to ext.cattle.io.selfuser and the Kubernetes access and token review requests, which are POSTs that do not change anything. Login and logout requests are kept. Combined with method, a request must match both"`
	MinResponseCode   int    `json:"minResponseCode,omitempty" jsonschema:"optional minimum HTTP response code (e.g. 400 to find failed requests)"`
	MaxResponseCode   int    `json:"maxResponseCode,omitempty" jsonschema:"optional maximum HTTP response code (e.g. 299 to find successful requests)"`
	Since             string `json:"since,omitempty" jsonschema:"optional RFC3339 timestamp, only requests made at or after it are returned (e.g. 2026-01-02T15:04:05Z)"`
	Until             string `json:"until,omitempty" jsonschema:"optional RFC3339 timestamp, only requests made at or before it are returned. Set it when paginating to limit shifting of the pages by new requests (timestamps have one-second precision, so requests logged in the same second may still shift them)"`
	Search            string `json:"search,omitempty" jsonschema:"optional case-insensitive text to search for anywhere in the entry, including the request and response bodies (e.g. a resource name or ID)"`
	IncludeBodies     bool   `json:"includeBodies,omitempty" jsonschema:"include larger request bodies and the response bodies. Defaults to false, which returns only the first 512 bytes of the request body. With true, request and response bodies are returned up to 8192 bytes each and longer ones are truncated. Use it with filters that select few entries, because bodies can be large"`
	Limit             int    `json:"limit,omitempty" jsonschema:"maximum number of entries to return, defaults to 50, at most 500"`
	Offset            int    `json:"offset,omitempty" jsonschema:"how many matching entries to skip, newest first, before returning results. Defaults to 0 and offset+limit can be at most 5000. To get the next page increase offset by limit, the response tells you the exact offset to use for the next page"`
}

// entryHeap is a min-heap of entries where the root is the oldest entry, i.e. the last in compareEntries order.
type entryHeap []entry

func (h entryHeap) Len() int           { return len(h) }
func (h entryHeap) Less(i, j int) bool { return compareEntries(&h[i], &h[j]) > 0 }
func (h entryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x any)        { *h = append(*h, x.(entry)) }
func (h *entryHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// searchResult holds the newest entries matching a search and problems reading the log.
type searchResult struct {
	// window is the maximum number of entries kept.
	window int
	// entries holds the newest window matching entries. It is a heap while the log is read and sorted
	// newest first once all logs have been read.
	entries entryHeap
	// total is the number of matching entries, including those not kept.
	total int
	// readOnlyReviews is the number of entries otherwise matching that changesOnly left out because they are
	// read-only review requests.
	readOnlyReviews int
	// warnings describe pods whose logs could not be read or parsed.
	warnings []string
}

// add records a matching entry, keeping only the newest window entries.
func (r *searchResult) add(e entry) {
	r.total++
	if len(r.entries) < r.window {
		heap.Push(&r.entries, e)
		return
	}
	if compareEntries(&e, &r.entries[0]) < 0 {
		r.entries[0] = e
		heap.Fix(&r.entries, 0)
	}
}

// searchAuditLogs searches the audit log written by the Rancher pods' audit log sidecar container.
func (t *Tools) searchAuditLogs(ctx context.Context, _ *mcp.CallToolRequest, params searchAuditLogsParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("searchAuditLogs called")

	f, err := newFilter(params)
	if err != nil {
		return nil, nil, err
	}
	offset, limit := max(params.Offset, 0), params.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	if offset > maxWindow-limit {
		return nil, nil, fmt.Errorf("offset+limit must be at most %d, use filters such as since, until, user, method or search to narrow the results instead", maxWindow)
	}

	clientset, err := t.client.CreateClientSet(ctx, middleware.Token(ctx), localCluster)
	if err != nil {
		zap.L().Error("failed to create clientset", zap.String("tool", "searchAuditLogs"), zap.Error(err))
		return nil, nil, err
	}

	result, err := t.search(ctx, clientset, podCacheKey(middleware.Token(ctx), localCluster), f, params.IncludeBodies, offset+limit)
	if err != nil {
		zap.L().Error("failed to search audit log", zap.String("tool", "searchAuditLogs"), zap.Error(err))
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponseAny(buildResponse(result, offset, limit, params.Until == ""))
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "searchAuditLogs"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// discoverPods returns the names of the pods in the Rancher namespace that have an audit log sidecar.
func discoverPods(ctx context.Context, clientset kubernetes.Interface) ([]string, error) {
	pods, err := clientset.CoreV1().Pods(rancherNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods in namespace %s: %w", rancherNamespace, err)
	}
	var names []string
	for _, pod := range pods.Items {
		if hasContainer(pod, auditLogContainer) {
			names = append(names, pod.Name)
		}
	}
	return names, nil
}

// search reads the audit log of every Rancher pod and returns the newest window matching entries, newest first.
// The pods are remembered per cacheKey for a short time. If reading a remembered pod fails, e.g. because the
// pod was replaced, the pods are listed again and the search is repeated once.
func (t *Tools) search(ctx context.Context, clientset kubernetes.Interface, cacheKey string, f filter, includeBodies bool, window int) (*searchResult, error) {
	logOptions := &corev1.PodLogOptions{Container: auditLogContainer}
	if !f.since.IsZero() {
		logOptions.SinceTime = &metav1.Time{Time: f.since.Add(-sinceMargin)}
	}

	for {
		pods, cached := t.pods.get(cacheKey)
		if !cached {
			var err error
			if pods, err = discoverPods(ctx, clientset); err != nil {
				return nil, err
			}
			t.pods.set(cacheKey, pods)
		}
		if len(pods) == 0 {
			return nil, fmt.Errorf("no Rancher pods with an %s container found in namespace %s. "+
				"The audit log can only be read when Rancher is installed with the Helm values auditLog.enabled=true and auditLog.destination=sidecar",
				auditLogContainer, rancherNamespace)
		}

		result := &searchResult{window: window}
		failed := 0
		for _, pod := range pods {
			if err := t.searchPod(ctx, clientset, pod, logOptions, f, includeBodies, result); err != nil {
				zap.L().Warn("unable to read audit log", zap.String("pod", pod), zap.Error(err))
				result.warnings = append(result.warnings, fmt.Sprintf("unable to read the audit log of pod %s: %v", pod, err))
				failed++
			}
		}

		if failed > 0 {
			// The remembered pods may be out of date, so do not use them for the next search either.
			t.pods.delete(cacheKey)
			if cached {
				zap.L().Debug("unable to read a remembered pod, listing the Rancher pods again")
				continue
			}
		}
		if failed == len(pods) {
			return nil, errors.New(strings.Join(result.warnings, "; "))
		}

		slices.SortFunc(result.entries, func(a, b entry) int { return compareEntries(&a, &b) })

		return result, nil
	}
}

// podStats counts the lines of a pod's log that were decoded and those that looked like audit log entries but
// could not be decoded.
type podStats struct {
	parsed   int
	unparsed int
}

// searchPod reads the audit log of a single pod and adds the matching entries to result.
// The log is streamed line by line so that only the newest matching entries are kept in memory.
func (t *Tools) searchPod(ctx context.Context, clientset kubernetes.Interface, pod string, opts *corev1.PodLogOptions, f filter, includeBodies bool, result *searchResult) error {
	stream, err := t.openLogs(ctx, clientset, rancherNamespace, pod, opts)
	if err != nil {
		return err
	}
	defer func() {
		if err := stream.Close(); err != nil {
			zap.L().Warn("failed to close audit log stream", zap.String("pod", pod), zap.Error(err))
		}
	}()

	var stats podStats
	reader := bufio.NewReader(stream)
	for lineNum := 0; ; lineNum++ {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			processLine(line, pod, lineNum, f, includeBodies, result, &stats)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("failed to read log stream: %w", readErr)
		}
	}

	if stats.unparsed > 0 {
		zap.L().Warn("unable to parse audit log lines", zap.String("pod", pod), zap.Int("unparsed", stats.unparsed), zap.Int("parsed", stats.parsed))
		result.warnings = append(result.warnings, fmt.Sprintf(
			"%d of %d audit log lines of pod %s could not be parsed and were skipped, the audit log format may not be supported",
			stats.unparsed, stats.unparsed+stats.parsed, pod))
	}

	return nil
}

// processLine decodes a log line and adds it to result if it matches the filter.
func processLine(line []byte, pod string, lineNum int, f filter, includeBodies bool, result *searchResult, stats *podStats) {
	if !f.matchesLine(line) {
		return
	}
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("{")) {
		// The sidecar may write lines that are not audit log entries (e.g. messages from tail), skip them.
		return
	}
	var raw rawEntry
	if err := json.Unmarshal(trimmed, &raw); err != nil || raw.AuditID == "" {
		stats.unparsed++
		return
	}
	stats.parsed++
	if !f.matches(&raw) {
		return
	}
	if f.isReadOnlyReview(&raw) {
		result.readOnlyReviews++
		return
	}
	result.add(compact(&raw, pod, lineNum, includeBodies))
}

func hasContainer(pod corev1.Pod, name string) bool {
	return slices.ContainsFunc(pod.Spec.Containers, func(c corev1.Container) bool { return c.Name == name })
}

// buildResponse paginates the matching entries and builds the data returned to the LLM. The result must hold
// at least the newest offset+limit matching entries. When suggestUntil is true and there are more pages, the
// note suggests setting until so that requests made while paginating do not shift the pages.
func buildResponse(result *searchResult, offset, limit int, suggestUntil bool) any {
	kept := len(result.entries)
	start := min(offset, kept)
	end := min(offset+limit, kept)
	page := result.entries[start:end]
	nextOffset := offset + len(page)

	var notes []string
	if offset > 0 || nextOffset < result.total {
		note := fmt.Sprintf("Returned %d audit log entries (offset %d, limit %d) out of %d matching entries, newest first. Use filters to narrow results.",
			len(page), offset, limit, result.total)
		switch {
		case nextOffset < result.total && nextOffset+limit <= maxWindow:
			note += fmt.Sprintf(" To get the next page, set offset=%d", nextOffset)
			if suggestUntil && kept > 0 {
				note += fmt.Sprintf(" and until=%s to limit shifting of the pages by new requests (timestamps have one-second precision, so requests logged in the same second may still shift them)", result.entries[0].RequestTimestamp.UTC().Format(time.RFC3339))
			}
			note += "."
		case nextOffset < result.total:
			// The next page would exceed the window, which the tool rejects, so do not suggest it.
			note += fmt.Sprintf(" There is no next page, a search can page through at most %d entries (offset+limit).", maxWindow)
			if kept > 0 {
				note += fmt.Sprintf(" To see older entries, search again with until=%s, or narrow the search with filters such as since, user, requestURI, changesOnly or search.", result.entries[kept-1].RequestTimestamp.UTC().Format(time.RFC3339))
			}
		}
		notes = append(notes, note)
	}
	if result.readOnlyReviews > 0 {
		notes = append(notes, fmt.Sprintf("changesOnly left out %d read-only review requests (e.g. ext.cattle.io.selfuser), which are POSTs that change nothing.", result.readOnlyReviews))
	}
	notes = append(notes, result.warnings...)

	data := map[string]any{"entries": page}
	if result.total == 0 {
		data["entries"] = "no audit log entries found matching the filters"
	}
	if len(notes) > 0 {
		data["note"] = strings.Join(notes, "\n")
	}

	return data
}
