package keepalivegate

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- unit parts (stubbed session, no transport) -----------------------------

type stubSession struct {
	mcp.Session
	id string
}

func (s *stubSession) ID() string { return s.id }

type stubRequest struct {
	mcp.Request
	sess mcp.Session
}

func (r *stubRequest) GetSession() mcp.Session { return r.sess }

// anyResult is a minimal mcp.Result for unit-level stub handlers (the SDK's
// own emptyResult is unexported; the gate never inspects result values).
type anyResult struct{ mcp.ResultBase }

func TestNonElicitMethodsPassThrough(t *testing.T) {
	calls := 0
	mw := Middleware()(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		calls++
		return &anyResult{}, nil
	})
	if _, err := mw(context.Background(), "tools/call", &stubRequest{sess: &stubSession{id: "s1"}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("pass-through failed (calls=%d)", calls)
	}
}

func TestElicitErrorReleasesGate(t *testing.T) {
	boom := errors.New("client gone")
	handler := Middleware()(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, boom
	})
	if _, err := handler(context.Background(), methodElicit, &stubRequest{sess: &stubSession{id: "s-x"}}); !errors.Is(err, boom) {
		t.Fatalf("expected elicitation error to propagate, got %v", err)
	}
	// After the error, a second elicitation on the same session must start
	// cleanly (counter released): have it return immediately.
	okHandler := Middleware()(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &anyResult{}, nil
	})
	if _, err := okHandler(context.Background(), methodElicit, &stubRequest{sess: &stubSession{id: "s-x"}}); err != nil {
		t.Fatalf("follow-up elicitation blocked after error: %v", err)
	}
}

func TestSessionsAreIsolated(t *testing.T) {
	// Per-session counters must never bleed across sessions: begin/done on s2
	// must not change s1's open count or cancel its watchdog.
	g := newGate()
	if wait := g.begin("s2"); wait > 0 {
		time.Sleep(wait)
	}
	if s := g.sessions["s2"]; s == nil || s.open != 1 {
		t.Fatalf("s2 open count wrong after begin: %+v", s)
	}
	if s := g.sessions["s1"]; s != nil && s.open != 0 {
		t.Fatalf("s1 got s2's open count: open=%d", s.open)
	}
	if s := g.sessions["s2"]; s != nil && s.cancelWatch != nil {
		t.Fatalf("s2 unexpectedly armed a watchdog without a real session")
	}
	// done on a session that never began must be a no-op (never panics,
	// never negative).
	g.done("s1")
	g.done("s2")
	if s := g.sessions["s2"]; s != nil {
		t.Fatalf("s2 entry leaked after done: %+v", s)
	}
}

// --- integration: real in-memory client<->server through the real SDK -------

// countingTransport wraps a Transport and counts outgoing JSON-RPC requests
// whose method is "ping" — i.e. server->client pings actually put on the wire.
type countingTransport struct {
	mcp.Transport
	mu        sync.Mutex
	pingCount int
}

func (t *countingTransport) pings() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pingCount
}

func (t *countingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &countingConnection{Connection: conn, ticks: &t.pingCount, mu: &t.mu}, nil
}

type countingConnection struct {
	mcp.Connection
	ticks *int
	mu    *sync.Mutex
}

// Write is called by the jsonrpc layer for every outbound message; count pings.
func (c *countingConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	if req, ok := msg.(*jsonrpc.Request); ok && req.Method == "ping" {
		c.mu.Lock()
		*c.ticks++
		c.mu.Unlock()
	}
	return c.Connection.Write(ctx, msg)
}

// TestWatchdogPingsOnlyWhileElicitationOpen drives a REAL server and client
// over an in-memory transport with the gate installed, then simulates an open
// dialog by arming the gate directly (white-box on the same Middleware) and
// verifies the wire-level invariants:
//
//  1. while the gate believes a dialog is open, real pings DO reach the
//     client (stream stays warm);
//  2. when the gate is released, pings STOP and the session keeps working
//     (no crash, no session kill).
//
// Why not drive a real elicitation: SDK default-protocol-2026-07-28 forbids
// standalone ServerSession.Elicit outside InputRequests (SEP-2322), and the
// production fork's clients all negotiate older protocol versions. Driving
// the gate directly exercises the same goroutine, the same Ping path, and
// the same handleSend type assertion that crashed alpha.7, without pinning
// an unexported client option.
func TestWatchdogPingsOnlyWhileElicitationOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("~70s of watchdog timings; run without -short")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ct, st := mcp.NewInMemoryTransports()
	serverCounting := &countingTransport{Transport: st}

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, &mcp.ServerOptions{})
	server.AddSendingMiddleware(Middleware())

	type args struct{}
	mcp.AddTool(server, &mcp.Tool{Name: "noop", Description: "no dialog"}, func(
		ctx context.Context, req *mcp.CallToolRequest, _ args) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})

	ss, err := server.Connect(ctx, serverCounting, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Baseline: no gate open -> expect complete silence (bounded small wait;
	// TestNoElicitationMeansNoPingsAtAll covers the full window).
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	silentBefore := serverCounting.pings()

	// White-box: begin one elicitation for this session by ID, exactly as
	// Middleware would on an outbound elicitation/create. We can't call
	// Middleware's private closure, but we can replicate the arm path via
	// begin+ensureWatchdog on the same gate — using the real ServerSession
	// returned by Connect ensures Ping goes through the full SDK send stack.
	// (No fake session here: this is the step that pins the alpha.7 panic
	// class.)
	gateUsed, ok := latestForTest.Load().(*gate)
	if !ok || gateUsed == nil {
		t.Fatal("gate not registered by Middleware")
	}

	if wait := gateUsed.begin(ss.ID()); wait > 0 {
		time.Sleep(wait)
	}
	gateUsed.ensureWatchdog(ctx, ss.ID(), ss)

	// ~2 intervals: pings must fire.
	time.Sleep(2*interval + 5*time.Second)
	during := serverCounting.pings() - silentBefore
	if during < 2 {
		t.Fatalf("pings while dialog open: got %d, want >=2", during)
	}

	// Release the gate; pings must stop.
	gateUsed.done(ss.ID())
	pingsAtClose := serverCounting.pings()
	time.Sleep(interval + 5*time.Second)
	if after := serverCounting.pings(); after != pingsAtClose {
		t.Fatalf("pings continued after dialog closed: %d -> %d", pingsAtClose, after)
	}

	// Session still healthy.
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatalf("session unusable after gate lifecycle: %v", err)
	}
}

// TestNoElicitationMeansNoPingsAtAll is the invariant the SDK's built-in
// keepalive broke for POST-only clients. With the gate installed and no
// dialog ever opened, the wire must stay completely silent and the session
// must survive well past the old "one missed pong" boundary.
func TestNoElicitationMeansNoPingsAtAll(t *testing.T) {
	if testing.Short() {
		t.Skip("~70s of watchdog timings")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ct, st := mcp.NewInMemoryTransports()
	serverCounting := &countingTransport{Transport: st}

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, &mcp.ServerOptions{})
	server.AddSendingMiddleware(Middleware())

	type args struct{}
	mcp.AddTool(server, &mcp.Tool{Name: "noop", Description: "no dialog"}, func(
		ctx context.Context, req *mcp.CallToolRequest, _ args) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})
	if _, err := server.Connect(ctx, serverCounting, nil); err != nil {
		t.Fatal(err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "noop", Arguments: args{}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2*interval + 5*time.Second)
	if p := serverCounting.pings(); p != 0 {
		t.Fatalf("silent session received %d server pings; want 0", p)
	}
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatalf("session died despite silence: %v", err)
	}
}
