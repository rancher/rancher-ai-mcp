// Package keepalivegate selectively suppresses the go-sdk's server keepalive
// pings so that only sessions with a pending elicitation keep their stream
// warm. Sessions without an open confirmation dialog never send pings — which
// is what protects clients that cannot answer them (because they never open
// the standalone SSE stream) from having their sessions killed by the SDK's
// ping-failure "close after one miss" logic.
//
// Background: the SDK's startKeepalive sends a server->client ping every
// interval and closes the session after a single missed pong. Ping delivery
// requires the standalone SSE stream; clients that only ever POST (dsh, most
// HTTP clients) never open it, so their first ping times out and their session
// is destroyed. But keepalive IS needed to hold a POST's SSE stream open
// across idle proxy timeouts while an elicitation dialog hangs. So: gate on
// elicitation.
package keepalivegate

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// The SDK keeps method names unexported; match by wire value.
	methodPing   = "ping"
	methodElicit = "elicitation/create"
)

// gate tracks pending elicitations per session ID and decides whether a given
// outgoing ping should be allowed onto the wire.
type gate struct {
	mu      sync.Mutex
	pending map[string]int // sessionID -> open elicitation count
}

func newGate() *gate {
	return &gate{pending: make(map[string]int)}
}

// inc records one more open elicitation for the session.
func (g *gate) inc(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pending[sessionID]++
}

// dec records an elicitation completing. Safe on unknown / zero sessions:
// the count never goes negative.
func (g *gate) dec(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending[sessionID] > 0 {
		g.pending[sessionID]--
	}
}

// allowed reports whether a ping to this session may go out.
func (g *gate) allowed(sessionID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pending[sessionID] > 0
}

// Middleware returns an mcp.Middleware (sending) implementing the gate:
//
//   - "ping" is forwarded only when the session has a pending elicitation;
//     otherwise it is swallowed with a synthetic success so the SDK's
//     keepalive loop sees no failure and does not close the session;
//   - "elicitation/create" increments the session's counter on the way out
//     and decrements it when the call completes (success or error);
//   - every other message passes through untouched.
//
// The counter is keyed by session ID because one MCP Server serves many
// sessions concurrently; pings must be gated per session, not globally.
func Middleware() mcp.Middleware {
	g := newGate()
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			sessionID := req.GetSession().ID()

			switch method {
			case methodPing:
				if !g.allowed(sessionID) {
					// Swallow: report success without touching the transport so the
					// SDK keepalive loop stays quiescent for this session. The SDK's
					// caller (ServerSession.Ping via handleSend[*emptyResult]) only
					// type-asserts the value, so any empty Result satisfies it and
					// nothing is written to the socket.
					return &pingOK{}, nil
				}
				return next(ctx, method, req)

			case methodElicit:
				g.inc(sessionID)
				defer g.dec(sessionID)
				return next(ctx, method, req)

			default:
				return next(ctx, method, req)
			}
		}
	}
}

// pingOK is the gate's synthetic successful-ping result, mirroring the SDK's
// unexported emptyResult ({} on the wire). ResultBase supplies the isResult
// marker and Meta accessors the mcp.Result interface requires.
type pingOK struct {
	mcp.ResultBase
}