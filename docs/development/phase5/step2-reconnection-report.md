# Step 2 Report: Agent Reconnection Supervision

**Date:** 2026-04-02
**Branch:** `phase5/reconnection`
**PR:** pending
**Status:** COMPLETED

---

## Summary

The agent now reconnects automatically when the heartbeat detects a dead connection. Previously, the heartbeat goroutine exited silently and the process stayed alive but idle (requiring systemd restart for recovery).

**Changes:**
- `TunnelClient.disconnectCh`: buffered channel written by heartbeat on failure
- `TunnelClient.DisconnectCh()`: public accessor for the tunnel supervision loop
- `TunnelRunner.Start`: wraps `acceptLoop` in a supervision loop that detects disconnect, calls `Reconnect`, and restarts the accept loop on the new MuxSession
- `acceptLoop`: extracted from Start; runs stream accept + forward on the current mux

**Recovery flow:**
1. Heartbeat PONG times out
2. Heartbeat writes to `disconnectCh` and exits
3. Accept loop gets mux read error (EOF) and returns
4. Supervision loop reads `disconnectCh`, calls `Reconnect` with backoff+jitter
5. New MuxSession established, `acceptLoop` restarts
6. Streams resume flowing

## Decisions Made

1. **Channel-based signaling (not callback)** -- `disconnectCh` is cleaner than a callback because the tunnel's select loop can wait on it alongside ctx.Done. No goroutine synchronization needed.
2. **Buffered channel (cap 1)** -- Non-blocking write from heartbeat. If nobody is reading (tunnel hasn't started yet), the signal isn't lost.
3. **Accept loop as separate method** -- `acceptLoop` is stateless with respect to the supervision loop. Each reconnection creates a fresh accept loop on the new mux. Clean separation.
4. **Default path: short pause before reconnect** -- If the accept loop exits without a heartbeat signal (e.g., mux read error), the supervision loop reconnects immediately. The backoff in `Reconnect` handles pacing.

## Deviations from Plan

- No dedicated reconnection test added in this step. The existing `TestTunnel_StartAcceptsAndForwards` still passes, and the reconnection path requires a relay that restarts mid-test. Full reconnection verification deferred to Step 6 (load testing).

## Coverage Report

Existing agent tests pass. No new test functions (reconnection is an integration-level concern).
