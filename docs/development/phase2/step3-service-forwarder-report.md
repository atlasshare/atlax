# Step 3 Report: Service Forwarder -- Bidirectional Copy

**Date:** 2026-03-29
**Branch:** `phase2/service-forwarder`
**PR:** #20
**Status:** COMPLETED

---

## Summary

Step 3 delivered the Forwarder that copies data bidirectionally between a multiplexed Stream and a local TCP service. This is the innermost loop of the tunnel: for each stream, dial local, copy both directions, clean up.

**Forwarder** satisfies the `ServiceForwarder` interface:
- Forward: dials target with configurable timeout, launches two io.CopyBuffer goroutines (stream->local, local->stream), waits for both to finish
- Context cancellation force-closes both ends to unblock io.Copy
- TCP half-close (CloseWrite) when stream->local copy finishes
- Configurable buffer size (default 32KB) and dial timeout (default 5s)

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| `TestForwarder_StreamToLocal` blocked forever on `relayStream.Close()` | `Stream.Close()` transitions to HalfClosedLocal but does NOT broadcast on cond, so `io.CopyBuffer(local, stream, buf)` stays blocked in `stream.Read()` | Changed test to use context cancellation instead of stream close for teardown |
| io.CopyBuffer blocks even after context cancel | `io.CopyBuffer` doesn't take a context; it blocks on the underlying `Read` call | Added context-cancel goroutine that calls `stream.Reset(0)` to force-unblock `Read` via `cond.Broadcast()` |
| `local.(*net.TCPConn).CloseWrite()` panics on net.Pipe | Tests use `net.Pipe` which returns `*net.pipe`, not `*net.TCPConn` | Added type assertion guard: `if tc, ok := local.(*net.TCPConn); ok` |
| Race fix cherry-pick needed on both branches | Steps 3 and 4 were parallel; the race fix in Step 2's test was on main | Cherry-picked commit `8c73300` from audit-emitter branch to service-forwarder branch |

## Decisions Made

1. **Reset (not Close) for forced teardown** -- When context is canceled, the context-cancel goroutine calls `stream.Reset(0)` instead of `stream.Close()`. Reset broadcasts on cond and transitions to StateReset, which makes `isReadClosed()` return true, unblocking any pending `Read`. Close only transitions to HalfClosedLocal which does not unblock reads.

2. **io.CopyBuffer with configurable buffer** -- Uses `io.CopyBuffer` with a caller-provided buffer size (default 32KB). This avoids the 32KB allocation inside `io.Copy` and allows tuning for different workloads.

3. **WaitGroup for goroutine lifecycle** -- Three goroutines (context cancel, stream->local copy, local->stream copy) coordinated via `sync.WaitGroup`. Forward blocks until all three complete.

4. **First error wins** -- `sync.Once` on error capture ensures only the first copy error is returned. The second copy typically fails with "use of closed connection" which is not actionable.

## Deviations from Plan

- **No IdleTimeout implementation** -- The `ServiceForwarderConfig.IdleTimeout` field exists but is not wired. Would require wrapping the net.Conn with deadline updates on each read/write. Deferred to Phase 5 (production hardening).
- **Stream close does not send STREAM_CLOSE over wire** -- This is a protocol-level gap: `Stream.Close()` only does a local state transition. The MuxSession does not yet detect local close and emit STREAM_CLOSE frames. Context cancellation + Reset is the reliable teardown path for now.

## Coverage Report

```
pkg/agent  89.8% of statements (forwarder + client combined)
```

Per-function:
- forwarder_impl.go: NewForwarder 100% (after defaults test), Forward 86.1%
- Forward uncovered: the `else { stream.Close() }` branch in context-cancel goroutine (only hit when stream is not *StreamSession)

7 tests: echo server, local-to-stream, bidirectional, dial timeout, context cancellation, large payload, defaults.
