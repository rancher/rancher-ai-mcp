package keepalivegate

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stubSession satisfies mcp.Session's exported surface for middleware tests.
// Only ID matters to the gate.
type stubSession struct {
	mcp.Session // embedded nil; gate never calls the unexported methods
	id    string
}

func (s *stubSession) ID() string { return s.id }

// stubRequest satisfies mcp.Request for use in middleware invocations.
type stubRequest struct {
	mcp.Request
	sess mcp.Session
}

func (r *stubRequest) GetSession() mcp.Session { return r.sess }

// recorder counts how often the wrapped handler ran.
type recorder struct{ calls int }

func (r *recorder) handler(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
	r.calls++
	return &pingOK{}, nil
}

func invoke(mw mcp.Middleware, h mcp.MethodHandler, method, sessionID string) error {
	wrapped := mw(h)
	_, err := wrapped(context.Background(), method, &stubRequest{sess: &stubSession{id: sessionID}})
	return err
}

func TestPingSwallowedWithNoElicitation(t *testing.T) {
	rec := &recorder{}
	if err := invoke(Middleware(), rec.handler, methodPing, "s1"); err != nil {
		t.Fatalf("swallowed ping returned error: %v", err)
	}
	if rec.calls != 0 {
		t.Fatalf("ping reached transport with no pending elicitation (calls=%d)", rec.calls)
	}
}

func TestPingAllowedDuringElicitation(t *testing.T) {
	rec := &recorder{}
	mw := Middleware()

	// Open an elicitation: wrap in a handler that pends until we let it return.
	release := make(chan struct{})
	elicited := make(chan struct{})
	go func() {
		_ = invoke(mw, func(ctx context.Context, m string, r mcp.Request) (mcp.Result, error) {
			close(elicited)
			<-release // hold the elicitation open
			return &pingOK{}, nil
		}, methodElicit, "s1")
	}()
	<-elicited

	if err := invoke(mw, rec.handler, methodPing, "s1"); err != nil {
		t.Fatalf("ping during elicitation errored: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("ping during elicitation was swallowed (calls=%d)", rec.calls)
	}
	close(release)
}

func TestPingSuppressedAgainAfterElicitationCloses(t *testing.T) {
	rec := &recorder{}
	mw := Middleware()

	// Open and close an elicitation.
	if err := invoke(mw, rec.handler, methodElicit, "s1"); err != nil {
		t.Fatalf("elicitation: %v", err)
	}
	before := rec.calls
	if err := invoke(mw, rec.handler, methodPing, "s1"); err != nil {
		t.Fatalf("post-elicitation ping errored: %v", err)
	}
	if rec.calls != before {
		t.Fatalf("ping after elicitation close was not swallowed (calls went %d -> %d)", before, rec.calls)
	}
}

func TestPingGateIsPerSession(t *testing.T) {
	rec := &recorder{}
	mw := Middleware()

	// Session s2 has an open elicitation; s1 has none.
	release := make(chan struct{})
	elicited := make(chan struct{})
	go func() {
		_ = invoke(mw, func(ctx context.Context, m string, r mcp.Request) (mcp.Result, error) {
			close(elicited)
			<-release
			return &pingOK{}, nil
		}, methodElicit, "s2")
	}()
	<-elicited
	defer close(release)

	// s1 ping must still be swallowed even though s2's elicitation is open.
	if err := invoke(mw, rec.handler, methodPing, "s1"); err != nil {
		t.Fatalf("s1 ping errored: %v", err)
	}
	if rec.calls != 0 {
		t.Fatalf("s1 ping leaked through gate while only s2 had elicitation open")
	}
}

func TestElicitErrorStillReleasesGate(t *testing.T) {
	rec := &recorder{}
	mw := Middleware()
	boom := errors.New("client gone")
	errElicit := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, boom
	}
	if err := invoke(mw, errElicit, methodElicit, "s1"); !errors.Is(err, boom) {
		t.Fatalf("expected elicitation error to propagate, got %v", err)
	}
	// Gate must be closed again: ping swallow.
	if err := invoke(mw, rec.handler, methodPing, "s1"); err != nil {
		t.Fatalf("ping errored: %v", err)
	}
	if rec.calls != 0 {
		t.Fatalf("ping not swallowed after failed elicitation")
	}
}

func TestNonGatedMethodsPassThrough(t *testing.T) {
	rec := &recorder{}
	if err := invoke(Middleware(), rec.handler, "tools/call", "s1"); err != nil {
		t.Fatalf("pass-through errored: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("non-gated method did not pass through")
	}
}
