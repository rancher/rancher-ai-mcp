// Package keepalivegate keeps POST-response SSE streams alive across
// idle proxy/ingress timeouts — but only while an elicitation dialog is
// open, which is the one phase where a response stream must survive a
// potentially minutes-long silence. At all other times no server→client
// pings are emitted at all, which is what protects clients that never
// open the standalone SSE stream (dsh, most HTTP clients) and cannot
// answer pings: no pings means no missed-pong kill.
//
// How it works: a sending middleware watches elicitation/create calls. On
// a session's first open elicitation, a watchdog starts sending real
// server→client pings (via the SDK's exported (*ServerSession).Ping) at a
// fixed interval; when the session's last open elicitation finishes, the
// watchdog exits and the session goes back to complete silence. In-flight
// elicitations are also frequency-throttled (minSpacing) so opens and
// closes of adjacent dialogs cannot let watches overlap or starve.
//
// This replaces two broken alternatives:
//   - SDK ServerOptions.KeepAlive (fork alpha.6): pings every session
//     unconditionally and closes the session after one missed pong. For
//     POST-only clients the ping can never be delivered, so every session
//     died ~1.5 intervals after creation (the chronic "session not
//     found").
//   - alpha.7's "swallow the ping" middleware: intercepting the SDK's
//     keepalive ping and returning a synthetic empty result. handleSend
//     type-asserts the concrete result type to the SDK's UNEXPORTED
//     *emptyResult, so any result a middleware can construct panics the
//     keepalive goroutine and crashes the whole server process. Never
//     fabricate mcp.Result values inside a middleware for a method the
//     SDK typed statically.
package keepalivegate

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// The SDK keeps method names unexported; match by wire value.
	methodElicit = "elicitation/create"

	// interval is the watchdog ping cadence. It must sit comfortably below
	// the ingress idle timeout (e.g. 360s) and
	// below the SDK's own client-side response deadline.
	interval = 30 * time.Second

	// minSpacing throttles successive elicitations on one session so
	// rapid open/close/open cannot overlap watchdogs.
	minSpacing = 2 * time.Second
)

// gate tracks open elicitations per session and owns each session's
// ping watchdog.
type gate struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	open        int       // elicitations currently in flight (incl. throttled ones)
	latestStart time.Time // start of the most recent elicitation (throttle ref)

	cancelWatch context.CancelFunc
}

func newGate() *gate {
	return &gate{sessions: make(map[string]*sessionState)}
}

// latestForTest records the most recently created gate so white-box tests in
// this repo can drive it directly (production code must not read it; it exists
// only so integration tests can arm the watchdog without driving a real
// elicitation, which SDK 2026-07-28 forbids outside InputRequests).
var latestForTest atomic.Value // *gate

// Middleware returns the sending middleware that drives the watchdogs.
// Call it once per server and install via Server.AddSendingMiddleware.
func Middleware() mcp.Middleware {
	g := newGate()
	latestForTest.Store(g)
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodElicit {
				return next(ctx, method, req)
			}

			session, ok := req.GetSession().(*mcp.ServerSession)
			if !ok {
				// Not a real server session (shouldn't happen in production);
				// pass through without gating.
				return next(ctx, method, req)
			}
			id := session.ID()

			// Throttle rapid successive elicitations on the same session.
			wait := g.begin(id)
			if wait > 0 {
				t := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					t.Stop()
					g.done(id)
					return nil, ctx.Err()
				case <-t.C:
				}
			}

			g.ensureWatchdog(ctx, id, session)

			res, err := next(ctx, method, req)

			g.done(id)
			return res, err
		}
	}
}

// begin registers one open elicitation and returns how long the caller
// must wait before starting it (throttle window).
func (g *gate) begin(id string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[id]
	if s == nil {
		s = &sessionState{}
		g.sessions[id] = s
	}
	var wait time.Duration
	if !s.latestStart.IsZero() {
		if d := time.Since(s.latestStart); d < minSpacing {
			wait = minSpacing - d
		}
	}
	s.latestStart = time.Now()
	s.open++
	return wait
}

// done registers one finished elicitation; when the count reaches zero the
// watchdog is stopped and the session goes silent again.
func (g *gate) done(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[id]
	if s == nil {
		return
	}
	if s.open > 0 {
		s.open--
	}
	if s.open == 0 {
		if s.cancelWatch != nil {
			s.cancelWatch()
			s.cancelWatch = nil
		}
		delete(g.sessions, id)
	}
}

// ensureWatchdog starts the session's ping watchdog on the first open
// elicitation. The watchdog pings on the real session: the SDK writes a
// genuine ping onto whatever stream is available (in production, the POST
// response SSE stream of the hanging tool call — exactly the stream that
// needs warming). Any ping error means the client/stream is gone, so the
// watchdog exits; it NEVER closes the session.
func (g *gate) ensureWatchdog(ctx context.Context, id string, session *mcp.ServerSession) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[id]
	if s == nil || s.cancelWatch != nil {
		return
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	s.cancelWatch = cancel
	go g.watchdog(watchCtx, id, session)
}

func (g *gate) watchdog(ctx context.Context, id string, session *mcp.ServerSession) {
	// Ping immediately: the interval between tool-call start and first
	// tick is exactly when a silent stream is most exposed to proxy idle
	// timeouts.
	ping := func() bool {
		pingCtx, cancel := context.WithTimeout(ctx, interval/2)
		defer cancel()
		if err := session.Ping(pingCtx, nil); err != nil {
			return false // client/stream gone; stop, but never touch the session
		}
		return true
	}
	if !ping() {
		return
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return // last elicitation finished; go silent
		case <-tick.C:
			if !ping() {
				return
			}
		}
	}
}
