# Step 2 Report: Agent Client -- Connection, Reconnection, Heartbeat

**Date:** 2026-03-29
**Branch:** `phase2/agent-client`
**PR:** #19
**Status:** COMPLETED

---

## Summary

Step 2 delivered the TunnelClient that manages the agent's persistent connection to the relay: initial connection, exponential backoff reconnection with jitter, and periodic heartbeat monitoring.

**TunnelClient** satisfies the `Client` interface:
- Connect: dials relay via Dialer interface, creates MuxSession(RoleAgent), starts heartbeat goroutine
- Reconnect: iterates with exponential backoff + jitter, caps at MaxInterval, respects context cancellation
- Close: graceful GoAway + teardown, idempotent
- Status: returns point-in-time snapshot including stream count from MuxSession

**BackoffConfig + ComputeBackoff**: Pure function for exponential backoff. Deterministic when jitter fraction is zero (testable). Formula: `base = initial * multiplier^attempt`, capped at max, jitter adds `rand * base * fraction`.

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| `agent.AgentClient` stutters (revive linter) | Go convention: package name + type name should not repeat | Renamed to `TunnelClient` |
| errcheck on `mux.GoAway(0)` in teardown | golangci-lint config has `check-blank: true` so `_ =` is also flagged | Used `//nolint:errcheck` -- GoAway during teardown is best-effort |
| goimports: wrong import grouping | Internal imports mixed with external | Separated into stdlib / external / internal groups |
| DATA RACE in `TestClient_HeartbeatDetectsDeadConnection` | `remoteConn` variable written in goroutine, read (as `_ = remoteConn`) in main goroutine without sync | Removed the dead read entirely |

## Decisions Made

1. **Dialer interface for testability** -- Production uses `TLSDialer` wrapping `crypto/tls.Dialer`. Tests inject `pipeDialer` using `net.Pipe()`. No real TLS in unit tests (auth package covers that).

2. **TunnelClient (not AgentClient)** -- Avoids `agent.AgentClient` stutter. External callers use the `Client` interface, not the concrete type.

3. **Heartbeat exits on failure but does not auto-reconnect** -- Separation of concerns: the heartbeat goroutine detects failure and exits. The caller (Tunnel or main) is responsible for triggering reconnection. This avoids recursive reconnection loops inside the client.

4. **BackoffConfig as pure function** -- No state, no timers. ComputeBackoff takes attempt number and returns a duration. The Reconnect method calls time.After with the computed duration. Easy to test deterministically with JitterFraction=0.

5. **relaySimulator helper in tests** -- Creates a MuxSession(RoleRelay) on the remote end of a pipe. Reused across all client tests.

## Deviations from Plan

- **Type renamed from AgentClient to TunnelClient** -- Linter-driven change. Interface `Client` is unchanged.
- **No CustomerID in Status** -- The client doesn't extract identity from the connection (that requires ExtractIdentity on a tls.Conn, but tests use net.Pipe). CustomerID population deferred to cmd/agent integration.

## Coverage Report

```
pkg/agent  90.5% of statements
```

Per-function:
- backoff.go: ComputeBackoff 100%, DefaultBackoffConfig 100%
- client_impl.go: Connect 90.9%, Reconnect 92.9%, Close 100%, Status 100%, Mux 100%, teardown 100%, runHeartbeat 90.9%
- TLSDialer.DialContext 0% (intentionally untested -- real TLS covered in pkg/auth)

20 tests, 1 benchmark.
