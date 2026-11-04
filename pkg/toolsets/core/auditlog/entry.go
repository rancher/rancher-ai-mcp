package auditlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// defaultBodyLimit is the maximum number of bytes of a request body returned by default.
	defaultBodyLimit = 512
	// fullBodyLimit is the maximum number of bytes of a request or response body returned when bodies are requested.
	fullBodyLimit = 8192
)

// rawIdentity is the identity of a user as written by Rancher when the audit-log-username feature is enabled.
type rawIdentity struct {
	Username    string `json:"username"`
	Provider    string `json:"provider"`
	PrincipalID string `json:"principalId"`
}

// rawEntry is a single line of the Rancher audit log. Only the fields used for filtering and
// output are decoded, request and response headers are ignored.
type rawEntry struct {
	AuditID    string `json:"auditID"`
	RequestURI string `json:"requestURI"`
	User       struct {
		Name     string              `json:"name"`
		Identity *rawIdentity        `json:"identity"`
		Group    []string            `json:"group"`
		Extra    map[string][]string `json:"extra"`
		// ImpersonatedBy is the real caller when the request impersonated another user.
		ImpersonatedBy *struct {
			Name     string       `json:"name"`
			Identity *rawIdentity `json:"identity"`
		} `json:"impersonatedBy"`
		RequestUser string `json:"requestUser"`
	} `json:"user"`
	// UserLoginName is the login name sent to a login endpoint, set when the request has no authenticated user.
	UserLoginName     string          `json:"userLoginName"`
	Method            string          `json:"method"`
	RemoteAddr        string          `json:"remoteAddr"`
	ResponseCode      int             `json:"responseCode"`
	RequestTimestamp  time.Time       `json:"requestTimestamp"`
	ResponseTimestamp time.Time       `json:"responseTimestamp"`
	RequestBody       json.RawMessage `json:"requestBody"`
	ResponseBody      json.RawMessage `json:"responseBody"`
}

// username returns the human-readable name of the user, preferring the identity over the user extras.
func (e *rawEntry) username() string {
	if e.User.Identity != nil && e.User.Identity.Username != "" {
		return e.User.Identity.Username
	}
	return first(e.User.Extra["username"])
}

// principalID returns the principal ID of the user, preferring the identity over the user extras.
func (e *rawEntry) principalID() string {
	if e.User.Identity != nil && e.User.Identity.PrincipalID != "" {
		return e.User.Identity.PrincipalID
	}
	return first(e.User.Extra["principalid"])
}

// impersonator is the real caller of an impersonated request returned to the LLM.
type impersonator struct {
	User        string `json:"user,omitempty"`
	Username    string `json:"username,omitempty"`
	Provider    string `json:"provider,omitempty"`
	PrincipalID string `json:"principalID,omitempty"`
}

// entry is the compact form of an audit log entry returned to the LLM.
type entry struct {
	AuditID           string        `json:"auditID"`
	RequestTimestamp  time.Time     `json:"requestTimestamp"`
	ResponseTimestamp time.Time     `json:"responseTimestamp,omitzero"`
	User              string        `json:"user,omitempty"`
	Username          string        `json:"username,omitempty"`
	Provider          string        `json:"provider,omitempty"`
	PrincipalID       string        `json:"principalID,omitempty"`
	ImpersonatedBy    *impersonator `json:"impersonatedBy,omitempty"`
	RequestUser       string        `json:"requestUser,omitempty"`
	UserLoginName     string        `json:"userLoginName,omitempty"`
	Method            string        `json:"method"`
	RequestURI        string        `json:"requestURI"`
	ResponseCode      int           `json:"responseCode"`
	RemoteAddr        string        `json:"remoteAddr,omitempty"`
	RequestBody       any           `json:"requestBody,omitempty"`
	ResponseBody      any           `json:"responseBody,omitempty"`
	Pod               string        `json:"pod,omitempty"`

	// line is the position of the entry in the log of its pod, used to order entries with the same timestamp.
	line int
}

// compareEntries orders entries newest first. Rancher writes timestamps with a precision of one second, so
// entries with the same timestamp from the same pod are ordered by their position in the log, which is the
// order in which they were written. Entries of different pods with the same timestamp are ordered by pod.
func compareEntries(a, b *entry) int {
	if c := b.RequestTimestamp.Compare(a.RequestTimestamp); c != 0 {
		return c
	}
	if c := strings.Compare(a.Pod, b.Pod); c != 0 {
		return c
	}
	return b.line - a.line
}

// filter selects audit log entries. Zero values match everything.
type filter struct {
	user            string
	methods         []string
	requestURI      string
	excludeURIs     []string
	changesOnly     bool
	minResponseCode int
	maxResponseCode int
	since           time.Time
	until           time.Time
	search          []byte
	// escapedSearch is search as it appears in a JSON string, e.g. with & written as \u0026.
	escapedSearch []byte
}

// newFilter builds a filter from the tool parameters, validating them.
func newFilter(params searchAuditLogsParams) (filter, error) {
	f := filter{
		user:            strings.ToLower(strings.TrimSpace(params.User)),
		requestURI:      strings.ToLower(params.RequestURI),
		minResponseCode: params.MinResponseCode,
		maxResponseCode: params.MaxResponseCode,
		changesOnly:     params.ChangesOnly,
	}
	if params.Search != "" {
		f.search = []byte(strings.ToLower(params.Search))
		f.escapedSearch = jsonEscape(f.search)
	}
	for u := range strings.SplitSeq(params.ExcludeRequestURI, ",") {
		if u = strings.ToLower(strings.TrimSpace(u)); u != "" {
			f.excludeURIs = append(f.excludeURIs, u)
		}
	}
	for m := range strings.SplitSeq(params.Method, ",") {
		if m = strings.ToUpper(strings.TrimSpace(m)); m != "" {
			f.methods = append(f.methods, m)
		}
	}

	var err error
	if params.Since != "" {
		if f.since, err = time.Parse(time.RFC3339, params.Since); err != nil {
			return filter{}, fmt.Errorf("invalid since %q, it must be an RFC3339 timestamp (e.g. 2026-01-02T15:04:05Z): %w", params.Since, err)
		}
	}
	if params.Until != "" {
		if f.until, err = time.Parse(time.RFC3339, params.Until); err != nil {
			return filter{}, fmt.Errorf("invalid until %q, it must be an RFC3339 timestamp (e.g. 2026-01-02T15:04:05Z): %w", params.Until, err)
		}
	}
	if !f.since.IsZero() && !f.until.IsZero() && f.until.Before(f.since) {
		return filter{}, fmt.Errorf("until %q is before since %q", params.Until, params.Since)
	}
	if f.maxResponseCode != 0 && f.maxResponseCode < f.minResponseCode {
		return filter{}, fmt.Errorf("maxResponseCode %d is less than minResponseCode %d", f.maxResponseCode, f.minResponseCode)
	}

	return f, nil
}

// jsonEscape returns s as it is written inside a JSON string by the Go encoder, which escapes quotes,
// backslashes, control characters and the HTML characters <, > and &. The hex digits of escapes are
// lower case, so the result can be compared with a lower cased log line.
func jsonEscape(s []byte) []byte {
	encoded, err := json.Marshal(string(s))
	if err != nil {
		return s
	}
	return encoded[1 : len(encoded)-1]
}

// matchesLine reports whether a raw log line can match the filter, before it is decoded.
// Decoding is the expensive part of reading the log, so lines not containing the search term are skipped early.
// The term is searched both as is and JSON escaped, because the line is encoded JSON.
func (f filter) matchesLine(line []byte) bool {
	if len(f.search) == 0 {
		return true
	}
	lower := bytes.ToLower(line)
	return bytes.Contains(lower, f.search) || bytes.Contains(lower, f.escapedSearch)
}

// changeMethods are the HTTP methods of requests that can change something.
var changeMethods = []string{"POST", "PUT", "PATCH", "DELETE"}

// readOnlyReviewURIs are substrings of the request URIs of create-only APIs that never change anything: they
// answer a question about the caller or a token. They are POST requests, so they match changeMethods.
var readOnlyReviewURIs = []string{
	"ext.cattle.io.selfuser",
	"selfsubjectreviews",
	"selfsubjectaccessreviews",
	"selfsubjectrulesreviews",
	"subjectaccessreviews",
	"tokenreviews",
}

// isReadOnlyReview reports whether the filter leaves out the entry because it is a read-only review request.
func (f filter) isReadOnlyReview(e *rawEntry) bool {
	if !f.changesOnly {
		return false
	}
	uri := strings.ToLower(e.RequestURI)
	return slices.ContainsFunc(readOnlyReviewURIs, func(x string) bool { return strings.Contains(uri, x) })
}

// matches reports whether a decoded entry matches the filter, except for the exclusion of read-only review
// requests by changesOnly which isReadOnlyReview reports, so that the entries left out can be counted.
func (f filter) matches(e *rawEntry) bool {
	if f.changesOnly && !slices.Contains(changeMethods, strings.ToUpper(e.Method)) {
		return false
	}
	if f.user != "" && !f.matchesUser(e) {
		return false
	}
	if len(f.methods) > 0 && !slices.Contains(f.methods, strings.ToUpper(e.Method)) {
		return false
	}
	if f.requestURI != "" && !strings.Contains(strings.ToLower(e.RequestURI), f.requestURI) {
		return false
	}
	if len(f.excludeURIs) > 0 {
		uri := strings.ToLower(e.RequestURI)
		if slices.ContainsFunc(f.excludeURIs, func(x string) bool { return strings.Contains(uri, x) }) {
			return false
		}
	}
	if f.minResponseCode != 0 && e.ResponseCode < f.minResponseCode {
		return false
	}
	if f.maxResponseCode != 0 && e.ResponseCode > f.maxResponseCode {
		return false
	}
	if !f.since.IsZero() && e.RequestTimestamp.Before(f.since) {
		return false
	}
	if !f.until.IsZero() && e.RequestTimestamp.After(f.until) {
		return false
	}
	return true
}

// matchesUser reports whether the user filter matches the user that made the request or the real caller that
// impersonated it, by user ID, username or principal ID, or the login name of a login request.
func (f filter) matchesUser(e *rawEntry) bool {
	candidates := []string{e.User.Name, e.UserLoginName}
	candidates = append(candidates, identityCandidates(e.User.Identity)...)
	candidates = append(candidates, e.User.Extra["username"]...)
	candidates = append(candidates, e.User.Extra["principalid"]...)
	if imp := e.User.ImpersonatedBy; imp != nil {
		candidates = append(candidates, imp.Name)
		candidates = append(candidates, identityCandidates(imp.Identity)...)
	}
	for _, c := range candidates {
		if c != "" && strings.ToLower(c) == f.user {
			return true
		}
	}
	return false
}

func identityCandidates(id *rawIdentity) []string {
	if id == nil {
		return nil
	}
	return []string{id.Username, id.PrincipalID}
}

// compact converts a raw entry into the form returned to the LLM.
func compact(e *rawEntry, pod string, line int, includeBodies bool) entry {
	out := entry{
		AuditID:           e.AuditID,
		RequestTimestamp:  e.RequestTimestamp,
		ResponseTimestamp: e.ResponseTimestamp,
		User:              e.User.Name,
		Username:          e.username(),
		PrincipalID:       e.principalID(),
		RequestUser:       e.User.RequestUser,
		UserLoginName:     e.UserLoginName,
		Method:            e.Method,
		RequestURI:        e.RequestURI,
		ResponseCode:      e.ResponseCode,
		RemoteAddr:        e.RemoteAddr,
		Pod:               pod,
		line:              line,
	}
	if e.User.Identity != nil {
		out.Provider = e.User.Identity.Provider
	}
	if imp := e.User.ImpersonatedBy; imp != nil {
		out.ImpersonatedBy = &impersonator{User: imp.Name}
		if imp.Identity != nil {
			out.ImpersonatedBy.Username = imp.Identity.Username
			out.ImpersonatedBy.Provider = imp.Identity.Provider
			out.ImpersonatedBy.PrincipalID = imp.Identity.PrincipalID
		}
	}
	limit := defaultBodyLimit
	if includeBodies {
		limit = fullBodyLimit
		out.ResponseBody = truncateBody(e.ResponseBody, limit)
	}
	out.RequestBody = truncateBody(e.RequestBody, limit)

	return out
}

// truncateBody returns the body as JSON if it fits in limit bytes, otherwise a truncated string of it.
// It returns nil for empty bodies so they are omitted from the output.
func truncateBody(body json.RawMessage, limit int) any {
	if len(body) == 0 || string(body) == "null" {
		return nil
	}
	if len(body) <= limit {
		return body
	}
	return fmt.Sprintf("%s... (truncated, %d of %d bytes shown)", strings.ToValidUTF8(string(body[:limit]), ""), limit, len(body))
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
