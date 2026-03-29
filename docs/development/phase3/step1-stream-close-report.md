# Step 1 Report: STREAM_CLOSE Wire Emission

**Date:** 2026-03-29
**Branch:** `phase3/stream-close`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Resolved the highest-priority Phase 2 carry-forward: Stream.Close() now emits a STREAM_CLOSE+FIN frame on the wire so the remote peer learns the stream closed gracefully, instead of relying on Reset (hard abort).

**Changes:**
- Added `onLocalClose` callback + `localCloseOnce` to StreamSession
- `SetOnLocalClose` setter called by MuxSession during stream registration
- `Close()` fires onLocalClose exactly once on HalfClosedLocal or Closed transition
- `setupStreamClose` in MuxSession enqueues STREAM_CLOSE+FIN and calls maybeRemoveStream
- Full stream lifecycle now works: open -> data -> close -> EOF -> close -> cleanup

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| FullStreamLifecycle test: agent.NumStreams() == 1 after both sides close | Agent's stream transitions to Closed via local Close() (HalfClosedRemote -> Closed), but maybeRemoveStream was only called from handleStreamClose/handleStreamData, not from the local close path | Added maybeRemoveStream call inside setupStreamClose callback, so streams are removed regardless of whether closure originates locally or remotely |

## Decisions Made

1. **onLocalClose callback pattern** -- Rather than having MuxSession poll stream states or having Close() directly enqueue frames (which would require the stream to know about the write queue), the stream calls a callback that MuxSession sets during registration. Clean separation: stream manages state, mux manages transport.

2. **localCloseOnce guarantees at-most-once** -- Double Close() is idempotent. The callback fires at most once regardless of how many times Close() transitions state.

3. **maybeRemoveStream in onLocalClose** -- When a stream transitions from HalfClosedRemote to Closed via local Close(), the stream is fully closed but no handleStreamClose will fire (it's a local event). The onLocalClose callback must also clean up the mux's stream map.

## Deviations from Plan

- **Relay config updates deferred** -- The plan called for CustomerConfig updates in this step. Moved to Step 4 (relay config) since they have no dependency on stream close and fit better there.

## Coverage Report

```
pkg/protocol  92.9% of statements
```

5 new tests: StreamCloseEmitsFrame, StreamCloseAgentSide, DoubleCloseNoDuplicate, CloseAfterResetNoFrame, FullStreamLifecycle.
