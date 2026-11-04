package auditlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

const fakeToken = "fakeToken"

// auditLine returns an audit log line in the format written by Rancher, including the headers that are
// not part of the output.
func auditLine(id, ts, user, username, method, uri string, code int, requestBody string) string {
	if requestBody == "" {
		requestBody = "null"
	}
	return fmt.Sprintf(`{"auditID":%q,"requestURI":%q,"user":{"name":%q,"group":["system:authenticated"],"extra":{"principalid":["local://%s"],"username":[%q]}},"method":%q,"remoteAddr":"10.0.0.1:1234","responseCode":%d,"requestTimestamp":%q,"responseTimestamp":%q,"requestHeader":{"Cookie":["[redacted]"]},"responseHeader":{"Content-Type":["application/json"]},"requestBody":%s,"responseBody":{"id":%q}}`,
		id, uri, user, user, username, method, code, ts, ts, requestBody, id)
}

var (
	lineGetUsers   = auditLine("a1", "2026-11-04T10:00:00Z", "user-admin", "admin", "GET", "/v1/management.cattle.io.users", 200, "")
	lineCreateUser = auditLine("a2", "2026-11-04T10:01:00Z", "user-admin", "admin", "POST", "/v1/management.cattle.io.users", 201, `{"username":"testing","displayName":"test user"}`)
	lineCreateGRB  = auditLine("a3", "2026-11-04T10:01:01Z", "user-admin", "admin", "POST", "/v3/globalrolebindings", 201, `{"globalRoleId":"user","userId":"u-95ckr"}`)
	lineForbidden  = auditLine("a4", "2026-11-04T10:02:00Z", "u-95ckr", "testing", "DELETE", "/v3/clusters/c-abc", 403, "")
	lineSelfUser   = auditLine("s1", "2026-11-04T10:01:02Z", "user-admin", "admin", "POST", "/v1/ext.cattle.io.selfuser", 201, "")
	lineOtherPod   = auditLine("b1", "2026-11-04T10:01:30Z", "u-kjlgj", "kevin", "PUT", "/v3/projects/local:p-abc", 200, `{"name":"p1"}`)

	// lineImpersonated is written when jane impersonated u-target with the audit-log-username feature enabled.
	lineImpersonated = `{"auditID":"i1","requestURI":"/v3/projects/local:p-abc","user":{"name":"u-target","identity":{"username":"target","provider":"local","principalId":"local://u-target"},"group":["system:authenticated"],"impersonatedBy":{"name":"u-jane","identity":{"username":"jane@example.com","provider":"keycloakoidc","principalId":"keycloakoidc_user://1234"}},"requestUser":"u-target"},"method":"DELETE","responseCode":204,"requestTimestamp":"2026-11-04T11:00:00Z"}`
	// lineLogin is a failed login, which has no authenticated user.
	lineLogin = `{"auditID":"l1","requestURI":"/v3-public/localProviders/local?action=login","userLoginName":"bob","method":"POST","responseCode":401,"requestTimestamp":"2026-11-04T11:00:00Z"}`
	// lineEscaped has a request URI with & escaped as the Go JSON encoder writes it.
	lineEscaped = `{"auditID":"e1","requestURI":"/v3/clusters?a=1\u0026b=2","user":{"name":"user-admin"},"method":"GET","responseCode":200,"requestTimestamp":"2026-11-04T11:00:00Z"}`

	largeBodyValue     = strings.Repeat("x", 600)
	lineLargeBody      = auditLine("big", "2026-11-04T11:00:00Z", "user-admin", "admin", "POST", "/v3/secrets", 201, `{"data":"`+largeBodyValue+`"}`)
	largeBodyTruncated = fmt.Sprintf(`"{\"data\":\"%s... (truncated, 512 of 611 bytes shown)"`, strings.Repeat("x", 512-len(`{"data":"`)))
)

func rancherPod(name string, containers ...string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: rancherNamespace,
			Labels:    map[string]string{"app": "rancher"},
		},
	}
	for _, c := range containers {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: c})
	}
	return pod
}

func TestSearchAuditLogs(t *testing.T) {
	rancher1 := rancherPod("rancher-1", "rancher", auditLogContainer)
	rancher2 := rancherPod("rancher-2", "rancher", auditLogContainer)
	logs := map[string]string{
		"rancher-1": strings.Join([]string{lineGetUsers, lineCreateUser, "tail: file truncated", lineCreateGRB, lineForbidden}, "\n") + "\n",
		// no trailing newline, the last line must still be read
		"rancher-2": lineOtherPod,
	}

	tests := map[string]struct {
		params         searchAuditLogsParams
		pods           []runtime.Object
		logs           map[string]string
		expectedResult string
		expectedError  string
		expectedSince  string
	}{
		"returns compact entries from all pods newest first": {
			params: searchAuditLogsParams{},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   logs,
			expectedResult: `{"llm": {"entries": [
				{"auditID":"a4","requestTimestamp":"2026-11-04T10:02:00Z","responseTimestamp":"2026-11-04T10:02:00Z","user":"u-95ckr","username":"testing","principalID":"local://u-95ckr","method":"DELETE","requestURI":"/v3/clusters/c-abc","responseCode":403,"remoteAddr":"10.0.0.1:1234","pod":"rancher-1"},
				{"auditID":"b1","requestTimestamp":"2026-11-04T10:01:30Z","responseTimestamp":"2026-11-04T10:01:30Z","user":"u-kjlgj","username":"kevin","principalID":"local://u-kjlgj","method":"PUT","requestURI":"/v3/projects/local:p-abc","responseCode":200,"remoteAddr":"10.0.0.1:1234","requestBody":{"name":"p1"},"pod":"rancher-2"},
				{"auditID":"a3","requestTimestamp":"2026-11-04T10:01:01Z","responseTimestamp":"2026-11-04T10:01:01Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v3/globalrolebindings","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"globalRoleId":"user","userId":"u-95ckr"},"pod":"rancher-1"},
				{"auditID":"a2","requestTimestamp":"2026-11-04T10:01:00Z","responseTimestamp":"2026-11-04T10:01:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v1/management.cattle.io.users","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"username":"testing","displayName":"test user"},"pod":"rancher-1"},
				{"auditID":"a1","requestTimestamp":"2026-11-04T10:00:00Z","responseTimestamp":"2026-11-04T10:00:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"GET","requestURI":"/v1/management.cattle.io.users","responseCode":200,"remoteAddr":"10.0.0.1:1234","pod":"rancher-1"}
			]}}`,
		},
		"filters by search text in the request body": {
			params: searchAuditLogsParams{Search: "TESTING", Method: "post"},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   logs,
			expectedResult: `{"llm": {"entries": [
				{"auditID":"a2","requestTimestamp":"2026-11-04T10:01:00Z","responseTimestamp":"2026-11-04T10:01:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v1/management.cattle.io.users","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"username":"testing","displayName":"test user"},"pod":"rancher-1"}
			]}}`,
		},
		"filters by username, response code and includes response bodies": {
			params: searchAuditLogsParams{User: "Testing", MinResponseCode: 400, IncludeBodies: true},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   logs,
			expectedResult: `{"llm": {"entries": [
				{"auditID":"a4","requestTimestamp":"2026-11-04T10:02:00Z","responseTimestamp":"2026-11-04T10:02:00Z","user":"u-95ckr","username":"testing","principalID":"local://u-95ckr","method":"DELETE","requestURI":"/v3/clusters/c-abc","responseCode":403,"remoteAddr":"10.0.0.1:1234","responseBody":{"id":"a4"},"pod":"rancher-1"}
			]}}`,
		},
		"filters by time range, method list and request URI": {
			params:        searchAuditLogsParams{Since: "2026-11-04T10:00:30Z", Until: "2026-11-04T10:01:59Z", Method: "POST, PUT", RequestURI: "GlobalRoleBindings"},
			pods:          []runtime.Object{rancher1, rancher2},
			logs:          logs,
			expectedSince: "2026-11-04T09:59:30Z",
			expectedResult: `{"llm": {"entries": [
				{"auditID":"a3","requestTimestamp":"2026-11-04T10:01:01Z","responseTimestamp":"2026-11-04T10:01:01Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v3/globalrolebindings","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"globalRoleId":"user","userId":"u-95ckr"},"pod":"rancher-1"}
			]}}`,
		},
		"excludes entries whose request URI contains any excluded substring": {
			params: searchAuditLogsParams{Method: "POST", ExcludeRequestURI: " EXT.cattle.io.selfuser, /v3/globalrolebindings,"},
			pods:   []runtime.Object{rancher1},
			logs: map[string]string{
				"rancher-1": strings.Join([]string{lineCreateUser, lineSelfUser, lineCreateGRB}, "\n") + "\n",
			},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"a2","requestTimestamp":"2026-11-04T10:01:00Z","responseTimestamp":"2026-11-04T10:01:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v1/management.cattle.io.users","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"username":"testing","displayName":"test user"},"pod":"rancher-1"}
			]}}`,
		},
		"changesOnly leaves out reads and read-only review requests and counts them": {
			params: searchAuditLogsParams{ChangesOnly: true},
			pods:   []runtime.Object{rancher1},
			logs: map[string]string{
				"rancher-1": strings.Join([]string{lineGetUsers, lineCreateUser, lineSelfUser, lineCreateGRB, lineLogin}, "\n") + "\n",
			},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"l1","requestTimestamp":"2026-11-04T11:00:00Z","userLoginName":"bob","method":"POST","requestURI":"/v3-public/localProviders/local?action=login","responseCode":401,"pod":"rancher-1"},
				{"auditID":"a3","requestTimestamp":"2026-11-04T10:01:01Z","responseTimestamp":"2026-11-04T10:01:01Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v3/globalrolebindings","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"globalRoleId":"user","userId":"u-95ckr"},"pod":"rancher-1"},
				{"auditID":"a2","requestTimestamp":"2026-11-04T10:01:00Z","responseTimestamp":"2026-11-04T10:01:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v1/management.cattle.io.users","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"username":"testing","displayName":"test user"},"pod":"rancher-1"}
			], "note": "changesOnly left out 1 read-only review requests (e.g. ext.cattle.io.selfuser), which are POSTs that change nothing."}}`,
		},
		"changesOnly combined with method requires both": {
			params: searchAuditLogsParams{ChangesOnly: true, Method: "DELETE,GET"},
			pods:   []runtime.Object{rancher1},
			logs: map[string]string{
				"rancher-1": strings.Join([]string{lineGetUsers, lineSelfUser, lineForbidden}, "\n") + "\n",
			},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"a4","requestTimestamp":"2026-11-04T10:02:00Z","responseTimestamp":"2026-11-04T10:02:00Z","user":"u-95ckr","username":"testing","principalID":"local://u-95ckr","method":"DELETE","requestURI":"/v3/clusters/c-abc","responseCode":403,"remoteAddr":"10.0.0.1:1234","pod":"rancher-1"}
			]}}`,
		},
		"paginates results": {
			params: searchAuditLogsParams{Method: "GET,POST", Limit: 1, Offset: 1},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   logs,
			expectedResult: `{"llm": {
				"entries": [
					{"auditID":"a2","requestTimestamp":"2026-11-04T10:01:00Z","responseTimestamp":"2026-11-04T10:01:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v1/management.cattle.io.users","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"username":"testing","displayName":"test user"},"pod":"rancher-1"}
				],
				"note": "Returned 1 audit log entries (offset 1, limit 1) out of 3 matching entries, newest first. Use filters to narrow results. To get the next page, set offset=2 and until=2026-11-04T10:01:01Z so that new requests do not shift the pages."
			}}`,
		},
		"paginates results without suggesting until when it is set": {
			params: searchAuditLogsParams{Method: "GET,POST", Until: "2026-11-04T10:01:01Z", Limit: 1},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   logs,
			expectedResult: `{"llm": {
				"entries": [
					{"auditID":"a3","requestTimestamp":"2026-11-04T10:01:01Z","responseTimestamp":"2026-11-04T10:01:01Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v3/globalrolebindings","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":{"globalRoleId":"user","userId":"u-95ckr"},"pod":"rancher-1"}
				],
				"note": "Returned 1 audit log entries (offset 0, limit 1) out of 3 matching entries, newest first. Use filters to narrow results. To get the next page, set offset=1."
			}}`,
		},
		"returns no entries for an offset past the end": {
			params: searchAuditLogsParams{Method: "GET,POST", Offset: 10, Limit: 5},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   logs,
			expectedResult: `{"llm": {
				"entries": [],
				"note": "Returned 0 audit log entries (offset 10, limit 5) out of 3 matching entries, newest first. Use filters to narrow results."
			}}`,
		},
		"caps limit at the maximum": {
			params: searchAuditLogsParams{Method: "DELETE", Offset: 1, Limit: 100000},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   logs,
			expectedResult: `{"llm": {
				"entries": [],
				"note": "Returned 0 audit log entries (offset 1, limit 500) out of 1 matching entries, newest first. Use filters to narrow results."
			}}`,
		},
		"error when offset+limit exceeds the maximum window": {
			params:        searchAuditLogsParams{Offset: 4900, Limit: 200},
			expectedError: "offset+limit must be at most 5000",
		},
		"returns identity and impersonator and filters by impersonator username": {
			params: searchAuditLogsParams{User: "JANE@example.com"},
			pods:   []runtime.Object{rancher1},
			logs:   map[string]string{"rancher-1": lineImpersonated + "\n" + lineGetUsers + "\n"},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"i1","requestTimestamp":"2026-11-04T11:00:00Z","user":"u-target","username":"target","provider":"local","principalID":"local://u-target","impersonatedBy":{"user":"u-jane","username":"jane@example.com","provider":"keycloakoidc","principalID":"keycloakoidc_user://1234"},"requestUser":"u-target","method":"DELETE","requestURI":"/v3/projects/local:p-abc","responseCode":204,"pod":"rancher-1"}
			]}}`,
		},
		"filters by identity principal ID": {
			params: searchAuditLogsParams{User: "local://u-target"},
			pods:   []runtime.Object{rancher1},
			logs:   map[string]string{"rancher-1": lineImpersonated + "\n" + lineGetUsers + "\n"},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"i1","requestTimestamp":"2026-11-04T11:00:00Z","user":"u-target","username":"target","provider":"local","principalID":"local://u-target","impersonatedBy":{"user":"u-jane","username":"jane@example.com","provider":"keycloakoidc","principalID":"keycloakoidc_user://1234"},"requestUser":"u-target","method":"DELETE","requestURI":"/v3/projects/local:p-abc","responseCode":204,"pod":"rancher-1"}
			]}}`,
		},
		"filters by the login name of an unauthenticated login request": {
			params: searchAuditLogsParams{User: "bob"},
			pods:   []runtime.Object{rancher1},
			logs:   map[string]string{"rancher-1": lineLogin + "\n" + lineGetUsers + "\n"},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"l1","requestTimestamp":"2026-11-04T11:00:00Z","userLoginName":"bob","method":"POST","requestURI":"/v3-public/localProviders/local?action=login","responseCode":401,"pod":"rancher-1"}
			]}}`,
		},
		"orders entries with the same timestamp by log position and pod": {
			params: searchAuditLogsParams{Method: "POST"},
			pods:   []runtime.Object{rancher1, rancher2},
			logs: map[string]string{
				// zz sorts after aa, so ordering by audit ID would put the create before the binding.
				"rancher-1": auditLine("zz", "2026-11-04T12:00:00Z", "user-admin", "admin", "POST", "/v3/users", 201, "") + "\n" +
					auditLine("aa", "2026-11-04T12:00:00Z", "user-admin", "admin", "POST", "/v3/globalrolebindings", 201, "") + "\n",
				"rancher-2": auditLine("mm", "2026-11-04T12:00:00Z", "user-admin", "admin", "POST", "/v3/projects", 201, "") + "\n",
			},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"aa","requestTimestamp":"2026-11-04T12:00:00Z","responseTimestamp":"2026-11-04T12:00:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v3/globalrolebindings","responseCode":201,"remoteAddr":"10.0.0.1:1234","pod":"rancher-1"},
				{"auditID":"zz","requestTimestamp":"2026-11-04T12:00:00Z","responseTimestamp":"2026-11-04T12:00:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v3/users","responseCode":201,"remoteAddr":"10.0.0.1:1234","pod":"rancher-1"},
				{"auditID":"mm","requestTimestamp":"2026-11-04T12:00:00Z","responseTimestamp":"2026-11-04T12:00:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v3/projects","responseCode":201,"remoteAddr":"10.0.0.1:1234","pod":"rancher-2"}
			]}}`,
		},
		"search matches characters that are escaped in the log": {
			params: searchAuditLogsParams{Search: "a=1&B=2"},
			pods:   []runtime.Object{rancher1},
			logs:   map[string]string{"rancher-1": lineEscaped + "\n" + lineGetUsers + "\n"},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"e1","requestTimestamp":"2026-11-04T11:00:00Z","user":"user-admin","method":"GET","requestURI":"/v3/clusters?a=1&b=2","responseCode":200,"pod":"rancher-1"}
			]}}`,
		},
		"warns about lines that cannot be parsed": {
			params: searchAuditLogsParams{Method: "PUT"},
			pods:   []runtime.Object{rancher2},
			logs: map[string]string{"rancher-2": lineOtherPod + "\n" +
				`{"auditID":"bad","requestTimestamp":"04/11/2026 10:00"}` + "\n" +
				`{"auditID":"cut","requestURI":"/v3/` + "\n"},
			expectedResult: `{"llm": {
				"entries": [
					{"auditID":"b1","requestTimestamp":"2026-11-04T10:01:30Z","responseTimestamp":"2026-11-04T10:01:30Z","user":"u-kjlgj","username":"kevin","principalID":"local://u-kjlgj","method":"PUT","requestURI":"/v3/projects/local:p-abc","responseCode":200,"remoteAddr":"10.0.0.1:1234","requestBody":{"name":"p1"},"pod":"rancher-2"}
				],
				"note": "2 of 3 audit log lines of pod rancher-2 could not be parsed and were skipped, the audit log format may not be supported"
			}}`,
		},
		"truncates large request bodies unless bodies are included": {
			params: searchAuditLogsParams{RequestURI: "/v3/secrets"},
			pods:   []runtime.Object{rancher1},
			logs:   map[string]string{"rancher-1": lineLargeBody + "\n"},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"big","requestTimestamp":"2026-11-04T11:00:00Z","responseTimestamp":"2026-11-04T11:00:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"POST","requestURI":"/v3/secrets","responseCode":201,"remoteAddr":"10.0.0.1:1234","requestBody":` + largeBodyTruncated + `,"pod":"rancher-1"}
			]}}`,
		},
		"no matching entries": {
			params:         searchAuditLogsParams{User: "nobody"},
			pods:           []runtime.Object{rancher1, rancher2},
			logs:           logs,
			expectedResult: `{"llm": {"entries": "no audit log entries found matching the filters"}}`,
		},
		"reports pods whose logs cannot be read": {
			params: searchAuditLogsParams{Method: "PUT"},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   map[string]string{"rancher-2": lineOtherPod},
			expectedResult: `{"llm": {
				"entries": [
					{"auditID":"b1","requestTimestamp":"2026-11-04T10:01:30Z","responseTimestamp":"2026-11-04T10:01:30Z","user":"u-kjlgj","username":"kevin","principalID":"local://u-kjlgj","method":"PUT","requestURI":"/v3/projects/local:p-abc","responseCode":200,"remoteAddr":"10.0.0.1:1234","requestBody":{"name":"p1"},"pod":"rancher-2"}
				],
				"note": "unable to read the audit log of pod rancher-1: logs not found"
			}}`,
		},
		"error when no pod has the audit log sidecar": {
			params:        searchAuditLogsParams{},
			pods:          []runtime.Object{rancherPod("rancher-1", "rancher")},
			expectedError: "auditLog.destination=sidecar",
		},
		"error when no logs can be read": {
			params:        searchAuditLogsParams{},
			pods:          []runtime.Object{rancher1},
			logs:          map[string]string{},
			expectedError: "unable to read the audit log of pod rancher-1: logs not found",
		},
		"error for invalid since": {
			params:        searchAuditLogsParams{Since: "yesterday"},
			expectedError: "invalid since",
		},
		"error when until is before since": {
			params:        searchAuditLogsParams{Since: "2026-11-04T10:00:00Z", Until: "2026-11-04T09:00:00Z"},
			expectedError: "is before since",
		},
		"error when max response code is less than min": {
			params:        searchAuditLogsParams{MinResponseCode: 400, MaxResponseCode: 299},
			expectedError: "maxResponseCode 299 is less than minResponseCode 400",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClientset := fake.NewClientset(tt.pods...)
			c := &client.Client{
				ClientSetCreator: func(_ *rest.Config) (kubernetes.Interface, error) { return fakeClientset, nil },
			}
			tools := NewTools(test.WrapClient(c, fakeToken))
			tools.openLogs = func(_ context.Context, _ kubernetes.Interface, namespace, pod string, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
				assert.Equal(t, rancherNamespace, namespace)
				assert.Equal(t, auditLogContainer, opts.Container)
				if tt.expectedSince == "" {
					assert.Nil(t, opts.SinceTime)
				} else if assert.NotNil(t, opts.SinceTime) {
					assert.Equal(t, tt.expectedSince, opts.SinceTime.UTC().Format("2006-01-02T15:04:05Z"))
				}
				content, ok := tt.logs[pod]
				if !ok {
					return nil, errors.New("logs not found")
				}
				return io.NopCloser(strings.NewReader(content)), nil
			}

			result, _, err := tools.searchAuditLogs(middleware.WithToken(t.Context(), fakeToken), &mcp.CallToolRequest{}, tt.params)
			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				assert.Nil(t, result)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tt.expectedResult, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

func TestTruncateBody(t *testing.T) {
	assert.Nil(t, truncateBody(nil, 10))
	assert.Nil(t, truncateBody([]byte("null"), 10))
	assert.JSONEq(t, `{"a":1}`, string(truncateBody([]byte(`{"a":1}`), 10).(json.RawMessage)))
	assert.Equal(t, `{"name":... (truncated, 8 of 18 bytes shown)`, truncateBody([]byte(`{"name":"abcdefg"}`), 8))
	// a multi-byte character cut in half is dropped rather than producing invalid UTF-8
	assert.Equal(t, `"... (truncated, 2 of 4 bytes shown)`, truncateBody([]byte(`"é"`), 2))
}

func TestAddTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	NewTools(test.WrapClient(&client.Client{}, fakeToken)).AddTools(server)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "client"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, "searchAuditLogs", tools.Tools[0].Name)
	assert.Equal(t, "rancher", tools.Tools[0].Meta["toolset"])
}

func TestSearchResultKeepsNewestEntries(t *testing.T) {
	result := &searchResult{window: 2}
	ts := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	for i, offset := range []int{3, 1, 5, 2, 4} {
		result.add(entry{AuditID: fmt.Sprint(offset), RequestTimestamp: ts.Add(time.Duration(offset) * time.Second), line: i})
	}
	slices.SortFunc(result.entries, func(a, b entry) int { return compareEntries(&a, &b) })

	assert.Equal(t, 5, result.total)
	require.Len(t, result.entries, 2)
	assert.Equal(t, "5", result.entries[0].AuditID)
	assert.Equal(t, "4", result.entries[1].AuditID)
}
