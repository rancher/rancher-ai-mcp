# Selective keepalive: gate server pings on pending elicitations

**Date:** 2026-10-20
**Status:** approved design (in-chat), implementing
**Owner:** fork

## Problem (corrected root cause)

The running pod `rancher-mcp-server-74584765d4-psc2p` has **`restartCount: 0` since 2026-10-10
02:06 UTC** — no overnight restart, no cert-rotation kill. The previous "pod restarts wipe
sessions" hypothesis is disproved.

The actual killer is the fork's own `MCP_KEEP_ALIVE=30s` default (image `v1.2.0-alpha.6`).
go-sdk v1.7.0 `startKeepalive` (`mcp/shared.go:852-890`) sends a server→client `ping` every
interval, gives the client `interval/2` (15 s) to answer, tolerates **one** failure, then calls
`session.Close()`. Ping delivery (`mcp/streamable.go` `Write`) lands on the **standalone SSE
stream**. Clients that only ever POST (dsh, baseline, most MCP HTTP clients) never open that
stream, so the ping cannot be delivered → times out → the server kills a live session and
removes it from the in-memory map → the client's next POST with the old `Mcp-Session-Id`
receives **404 session not found**. For non-ponging clients, alpha.6 turned every session into
a ~45-second death sentence.

Why the flag exists at all: it was added to keep Claude Code's SSE stream alive across the
ingress's idle timeout **while an elicitation dialog is hanging open** (the only moment the
server→client stream must survive long silences). So keepalive is genuinely needed — but only
in that window.

## Design

Gate the ping on whether the session has a **pending elicitation**.

- `cmd/serve.go` keeps `mcp.NewServer(..., &mcp.ServerOptions{KeepAlive: keepAlive})` so the
  SDK spawns its per-session keepalive goroutine as usual.
- A small package (`pkg/keepalivegate`) exposes a per-session atomic counter and an
  `mcp.Middleware` (sending) that:
  1. passes `ping` through **only when that session's counter > 0**; otherwise swallows it
     (returns a zero-value `PingResult`, never touching the socket — no traffic, no failure
     counter, no session kill);
  2. increments the counter when it sees `elicitation/create` go out, and decrements it when
     that call returns (wraps `next` in a `defer` — result *or* error, both decrement).
- Sessions are keyed by their `ServerSession` pointer (safe: the counter is per-session by
  construction because middleware receives the session-bound `Request`).
- No vendoring, no SDK patch. Uses only public API: `Server.AddSendingMiddleware`,
  `Middleware`, `MethodHandler`, `ServerRequest`.

### Behavior matrix

| Client / phase | counter | ping | outcome |
|---|---|---|---|
| plain POST client (dsh), normal calls | 0 | swallowed | session never killed; 404 gone |
| Claude Code, no dialog open | 0 | swallowed | same safety as upstream stock |
| Claude Code, `executeChange` dialog open | ≥1 | sent every 30 s | SSE stream stays warm through ingress idle timeout (original fix preserved) |
| dialog answered / cancelled / errored | back to 0 | swallowed | stream goes quiet again |

### Accepted boundaries (non-issues)

1. If the client dies while a dialog is open, the counter stays > 0 until the tool handler's
   context ends; worst case the server fires a few wasted pings at a dead client for one
   interval. No correctness impact, no cross-session impact.
2. The SDK's keepalive goroutine keeps running per session either way — it just becomes a
   no-op when the gate swallows its pings. Goroutine cost per session is negligible.

## What this does NOT change

- `confirm.Gate` (token issue/consume/TTL/nonce) untouched — this is transport-layer only.
- `MCP_KEEP_ALIVE` env / `--keep-alive` flag semantics stay; operators can still disable
  entirely with `0`.
- No image rebuild or rollout is triggered by the code change itself.

## Follow-ups (not in scope)

- `confirm.Gate` HMAC key is generated per process start and `TokenTTL` is 10 min; any pod
  restart invalidates outstanding confirmation tokens ("bad signature"). Separate, independent
  fork robustness item.
- Clients (dsh) still worth fixing their own 404→re-init; that protects them against any other
  server-side session loss. But the server must stop killing live sessions first.
