package auditlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
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
			// Labels depend on the name of the Helm release, e.g. app=prod-rancher, so they are not used.
			Labels: map[string]string{"app": name + "-release"},
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
		"finds pods by their sidecar whatever the labels and ignores other pods": {
			params: searchAuditLogsParams{},
			pods: []runtime.Object{
				rancher1,
				rancherPod("helm-operation-abc", "helm", "proxy"),
				rancherPod("rancher-webhook-1", "rancher-webhook"),
			},
			logs: map[string]string{"rancher-1": lineGetUsers + "\n"},
			expectedResult: `{"llm": {"entries": [
				{"auditID":"a1","requestTimestamp":"2026-11-04T10:00:00Z","responseTimestamp":"2026-11-04T10:00:00Z","user":"user-admin","username":"admin","principalID":"local://user-admin","method":"GET","requestURI":"/v1/management.cattle.io.users","responseCode":200,"remoteAddr":"10.0.0.1:1234","pod":"rancher-1"}
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
				"note": "Returned 1 audit log entries (offset 1, limit 1) out of 3 matching entries, newest first. Use filters to narrow results. To get the next page, set offset=2 and until=2026-11-04T10:01:01Z to limit shifting of the pages by new requests (timestamps have one-second precision, so requests logged in the same second may still shift them)."
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
		"error when offset+limit would overflow": {
			params:        searchAuditLogsParams{Offset: math.MaxInt},
			expectedError: "offset+limit must be at most 5000",
		},
		"error when offset is the maximum and limit is capped": {
			params:        searchAuditLogsParams{Offset: math.MaxInt, Limit: math.MaxInt},
			expectedError: "offset+limit must be at most 5000",
		},
		"allows offset+limit equal to the maximum window": {
			params: searchAuditLogsParams{Method: "GET,POST", Offset: 4500, Limit: 500},
			pods:   []runtime.Object{rancher1, rancher2},
			logs:   logs,
			expectedResult: `{"llm": {
				"entries": [],
				"note": "Returned 0 audit log entries (offset 4500, limit 500) out of 3 matching entries, newest first. Use filters to narrow results."
			}}`,
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

func TestBuildResponseNeverSuggestsUnusableOffset(t *testing.T) {
	entries := make(entryHeap, maxWindow)
	start := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for i := range entries {
		// newest first
		entries[i] = entry{AuditID: fmt.Sprintf("e%d", i), RequestTimestamp: start.Add(-time.Duration(i) * time.Second)}
	}
	result := &searchResult{window: maxWindow, entries: entries, total: maxWindow + 1000}

	tests := map[string]struct {
		offset, limit int
		contains      []string
		notContains   []string
	}{
		"suggests the next page while it fits in the window": {
			offset: 0, limit: 100,
			contains: []string{"set offset=100"},
		},
		"suggests the last page that fits in the window": {
			offset: 4400, limit: 100,
			contains: []string{"set offset=4500"},
		},
		"does not suggest an offset at the window boundary": {
			offset: 4500, limit: 500,
			contains:    []string{"no next page", "at most 5000", "until=" + start.Add(-(maxWindow-1)*time.Second).Format(time.RFC3339)},
			notContains: []string{"set offset="},
		},
		"does not suggest an offset whose page would exceed the window": {
			offset: 4500, limit: 300,
			contains:    []string{"no next page"},
			notContains: []string{"set offset="},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			data, ok := buildResponse(result, tt.offset, tt.limit, true).(map[string]any)
			require.True(t, ok)
			note, _ := data["note"].(string)
			for _, c := range tt.contains {
				assert.Contains(t, note, c)
			}
			for _, c := range tt.notContains {
				assert.NotContains(t, note, c)
			}
		})
	}
}

func TestCompactSkipsEmptyUserExtras(t *testing.T) {
	tests := map[string]struct {
		extra               string
		expectedUsername    string
		expectedPrincipalID string
	}{
		"single values": {
			extra:               `{"username":["admin"],"principalid":["local://user-admin"]}`,
			expectedUsername:    "admin",
			expectedPrincipalID: "local://user-admin",
		},
		"skips empty values before the first real value": {
			extra:               `{"username":["","admin"],"principalid":["","local://user-admin"]}`,
			expectedUsername:    "admin",
			expectedPrincipalID: "local://user-admin",
		},
		"only empty or missing values": {
			extra: `{"username":[""]}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			line := fmt.Sprintf(`{"auditID":"x","requestURI":"/v3/users","user":{"name":"user-admin","extra":%s},"method":"POST","responseCode":201,"requestTimestamp":"2026-11-04T10:00:00Z"}`, tt.extra)
			var raw rawEntry
			require.NoError(t, json.Unmarshal([]byte(line), &raw))

			got := compact(&raw, "rancher-1", 0, false)

			assert.Equal(t, tt.expectedUsername, got.Username)
			assert.Equal(t, tt.expectedPrincipalID, got.PrincipalID)
		})
	}
}

// newCachingTools returns tools reading the logs of the pods in logs and a counter of the pod list calls.
func newCachingTools(t *testing.T, logs map[string]string, pods ...runtime.Object) (*Tools, *int) {
	t.Helper()
	return cachingToolsFor(t, logs, fake.NewClientset(pods...))
}

// cachingToolsFor is newCachingTools for a clientset the test also uses to change the pods or reactors.
func cachingToolsFor(t *testing.T, logs map[string]string, fakeClientset *fake.Clientset) (*Tools, *int) {
	t.Helper()
	lists := 0
	fakeClientset.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists++
		return false, nil, nil
	})
	c := &client.Client{
		ClientSetCreator: func(_ *rest.Config) (kubernetes.Interface, error) { return fakeClientset, nil },
	}
	tools := NewTools(test.WrapClient(c, fakeToken))
	tools.openLogs = func(_ context.Context, _ kubernetes.Interface, _, pod string, _ *corev1.PodLogOptions) (io.ReadCloser, error) {
		content, ok := logs[pod]
		if !ok {
			return nil, apierrors.NewNotFound(corev1.Resource("pods"), pod)
		}
		return io.NopCloser(strings.NewReader(content)), nil
	}
	return tools, &lists
}

func search(t *testing.T, tools *Tools, token string) (*mcp.CallToolResult, error) {
	t.Helper()
	result, _, err := tools.searchAuditLogs(middleware.WithToken(t.Context(), token), &mcp.CallToolRequest{}, searchAuditLogsParams{})
	return result, err
}

func TestSearchRemembersPodsForTTL(t *testing.T) {
	// the bubble has a fake clock that advances when every goroutine is blocked, so the sleep is instant
	synctest.Test(t, func(t *testing.T) {
		logs := map[string]string{"rancher-1": lineGetUsers + "\n"}
		tools, lists := newCachingTools(t, logs, rancherPod("rancher-1", "rancher", auditLogContainer))

		for range 3 {
			_, err := search(t, tools, fakeToken)
			require.NoError(t, err)
		}
		assert.Equal(t, 1, *lists, "pods are listed once while the entry is valid")

		time.Sleep(podCacheTTL + time.Second)
		_, err := search(t, tools, fakeToken)
		require.NoError(t, err)
		assert.Equal(t, 2, *lists, "pods are listed again after the entry expired")
	})
}

// anyTokenClient creates the clientset for any token.
type anyTokenClient struct{ clientset kubernetes.Interface }

func (c anyTokenClient) CreateClientSet(context.Context, string, string) (kubernetes.Interface, error) {
	return c.clientset, nil
}

func TestSearchDoesNotShareRememberedPodsBetweenTokens(t *testing.T) {
	fakeClientset := fake.NewClientset(rancherPod("rancher-1", "rancher", auditLogContainer))
	lists := 0
	fakeClientset.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists++
		return false, nil, nil
	})
	tools := NewTools(anyTokenClient{fakeClientset})
	tools.openLogs = func(context.Context, kubernetes.Interface, string, string, *corev1.PodLogOptions) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(lineGetUsers + "\n")), nil
	}

	for _, token := range []string{"token-a", "token-b", "token-a", "token-b"} {
		_, err := search(t, tools, token)
		require.NoError(t, err)
	}
	assert.Equal(t, 2, lists, "every token lists the pods itself, once")

	assert.NotEqual(t, podCacheKey("token-a", localCluster), podCacheKey("token-b", localCluster))
	assert.NotContains(t, podCacheKey("token-a", localCluster), "token-a")
}

func TestSearchListsPodsAgainWhenRememberedPodCannotBeRead(t *testing.T) {
	// rancher-old is remembered but was replaced by rancher-new
	logs := map[string]string{"rancher-new": lineGetUsers + "\n"}
	tools, lists := newCachingTools(t, logs, rancherPod("rancher-new", "rancher", auditLogContainer))
	tools.pods.set(podCacheKey(fakeToken, localCluster), []string{"rancher-old"})

	result, err := search(t, tools, fakeToken)

	require.NoError(t, err)
	assert.Equal(t, 1, *lists)
	text := result.Content[0].(*mcp.TextContent).Text
	assert.Contains(t, text, `"pod":"rancher-new"`)
	assert.NotContains(t, text, "unable to read")
}

func TestSearchForgetsPodsWhenAFreshlyListedPodCannotBeRead(t *testing.T) {
	// rancher-2 exists but its log cannot be read, so the search returns the entries of rancher-1 with a warning
	logs := map[string]string{"rancher-1": lineGetUsers + "\n"}
	tools, lists := newCachingTools(t, logs,
		rancherPod("rancher-1", "rancher", auditLogContainer),
		rancherPod("rancher-2", "rancher", auditLogContainer))

	result, err := search(t, tools, fakeToken)
	require.NoError(t, err)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "unable to read the audit log of pod rancher-2")
	assert.Equal(t, 1, *lists)
	_, remembered := tools.pods.get(podCacheKey(fakeToken, localCluster))
	assert.False(t, remembered, "a pod that could not be read must not be remembered")

	// the next search lists the pods again instead of waiting for the entry to expire
	_, err = search(t, tools, fakeToken)
	require.NoError(t, err)
	assert.Equal(t, 2, *lists)
}

func TestSearchDoesNotRememberNoPods(t *testing.T) {
	fakeClientset := fake.NewClientset(rancherPod("rancher-1", "rancher"))
	c := &client.Client{
		ClientSetCreator: func(_ *rest.Config) (kubernetes.Interface, error) { return fakeClientset, nil },
	}
	tools := NewTools(test.WrapClient(c, fakeToken))
	tools.openLogs = func(context.Context, kubernetes.Interface, string, string, *corev1.PodLogOptions) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(lineGetUsers + "\n")), nil
	}

	_, err := search(t, tools, fakeToken)
	require.ErrorContains(t, err, "no Rancher pods with an rancher-audit-log container")

	// the audit log sidecar is enabled, the next search must find it without waiting for the TTL
	_, err = fakeClientset.CoreV1().Pods(rancherNamespace).Create(t.Context(), rancherPod("rancher-2", "rancher", auditLogContainer), metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = search(t, tools, fakeToken)
	require.NoError(t, err)
}

func TestSearchDoesNotRememberPodsWhenListingFails(t *testing.T) {
	fakeClientset := fake.NewClientset(rancherPod("rancher-1", "rancher", auditLogContainer))
	failList := true
	fakeClientset.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		if failList {
			return true, nil, errors.New("api unavailable")
		}
		return false, nil, nil
	})
	tools, lists := cachingToolsFor(t, map[string]string{"rancher-1": lineGetUsers + "\n"}, fakeClientset)

	_, err := search(t, tools, fakeToken)
	require.ErrorContains(t, err, "failed to list pods in namespace cattle-system")
	assert.Equal(t, 1, *lists)

	// the next search lists the pods again and works, a failure is not remembered
	failList = false
	result, err := search(t, tools, fakeToken)
	require.NoError(t, err)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"pod":"rancher-1"`)
	assert.Equal(t, 2, *lists)
}

// A pod that starts while the pods are remembered is not searched until the entry expires. This documents the
// trade-off of remembering the pods for podCacheTTL.
func TestSearchFindsNewPodOnlyAfterTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fakeClientset := fake.NewClientset(rancherPod("rancher-1", "rancher", auditLogContainer))
		logs := map[string]string{
			"rancher-1": lineGetUsers + "\n",
			"rancher-2": lineCreateUser + "\n",
		}
		tools, lists := cachingToolsFor(t, logs, fakeClientset)

		result, err := search(t, tools, fakeToken)
		require.NoError(t, err)
		assert.NotContains(t, result.Content[0].(*mcp.TextContent).Text, `"pod":"rancher-2"`)

		// rancher-2 starts, e.g. in a rollout
		_, err = fakeClientset.CoreV1().Pods(rancherNamespace).Create(t.Context(), rancherPod("rancher-2", "rancher", auditLogContainer), metav1.CreateOptions{})
		require.NoError(t, err)

		result, err = search(t, tools, fakeToken)
		require.NoError(t, err)
		assert.NotContains(t, result.Content[0].(*mcp.TextContent).Text, `"pod":"rancher-2"`, "remembered pods are used until the entry expires")
		assert.Equal(t, 1, *lists)

		time.Sleep(podCacheTTL + time.Second)
		result, err = search(t, tools, fakeToken)
		require.NoError(t, err)
		text := result.Content[0].(*mcp.TextContent).Text
		assert.Contains(t, text, `"pod":"rancher-1"`)
		assert.Contains(t, text, `"pod":"rancher-2"`)
		assert.Equal(t, 2, *lists)
	})
}

func TestPodCacheIsBounded(t *testing.T) {
	c := newPodCache(time.Hour)
	for i := range podCacheMaxEntries * 3 {
		c.set(fmt.Sprintf("key-%d", i), []string{"rancher-1"})
	}
	assert.LessOrEqual(t, len(c.entries), podCacheMaxEntries)

	// the entry just written is kept
	c.set("latest", []string{"rancher-1"})
	_, ok := c.get("latest")
	assert.True(t, ok)
}
