# Step 4 Report: Audit Emitter -- Community Implementation

**Date:** 2026-03-29
**Branch:** `phase2/audit-emitter`
**PR:** #21
**Status:** COMPLETED

---

## Summary

Step 4 delivered the community edition audit emitter: an async, structured JSON logger backed by `log/slog`. Events are buffered in a channel and drained by a background goroutine, so `Emit` never blocks the caller.

**SlogEmitter** satisfies the `Emitter` interface:
- Emit: non-blocking send to buffered channel (default capacity 256)
- Close: closes channel, waits for drain goroutine to flush all pending events, idempotent via sync.Once
- Returns `ErrEmitterClosed` if Emit is called after Close

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| gocritic hugeParam on Event (112 bytes) | Event has 7 fields including a string map | Added `//nolint:gocritic` -- Event is an immutable value type; the Emitter interface requires value semantics |
| gofmt formatting after nolint comment | nolint directive as separate line above func caused gofmt to reformat | Moved nolint to doc comment block above the function |
| **CI panic: send on closed channel in Emit** | `Close()` called `close(eventCh)` while `Emit`'s select could still pick the `eventCh <-` case. In Go, a select with a send on a closed channel panics. The `<-e.done` case was supposed to win, but channel scheduling is non-deterministic -- there is no priority ordering in select. | Added a separate `closeCh` channel. `Close()` closes `closeCh` first, then `eventCh`. `Emit` checks `closeCh` in a non-blocking default select before attempting to send on `eventCh`. This guarantees Emit sees the close signal before the channel is closed. Fixed in commit `680010c`. |

### Detailed Analysis: send-on-closed-channel panic

The original code had:

```go
// Emit
select {
case e.eventCh <- event:  // PANIC if eventCh is closed
    return nil
case <-e.done:
    return ErrEmitterClosed
}

// Close
close(e.eventCh)  // drainLoop exits
<-e.done           // wait for flush
```

The race window: `Close()` calls `close(e.eventCh)`. Before `drainLoop` finishes and closes `e.done`, a concurrent `Emit` enters the select. Both cases are ready (send on closed channel, and done is not yet closed), so Go picks one randomly. If it picks the send case, panic.

The fix uses a two-phase close with a separate signal channel:

```go
// Emit
select {
case <-e.closeCh:           // fast path: already closed
    return ErrEmitterClosed
default:
}
select {
case e.eventCh <- event:
    return nil
case <-e.closeCh:           // closed before eventCh
    return ErrEmitterClosed
}

// Close
close(e.closeCh)  // 1. signal Emit to stop
close(e.eventCh)  // 2. signal drainLoop to exit
<-e.done          // 3. wait for flush
```

`closeCh` is always closed before `eventCh`, so Emit sees the close signal and returns `ErrEmitterClosed` before the eventCh send case can trigger a panic. The first select with `default` is a fast-path optimization to avoid entering the second select when already closed.

**Lesson:** Never close a channel that has concurrent senders. Use a separate signal channel to coordinate shutdown, then close the data channel only after all senders have stopped.

## Decisions Made

1. **Buffered channel (not sync queue)** -- Channel semantics are simpler for single-producer-multiple-consumer patterns. Buffer size of 256 prevents backpressure under normal load. If the buffer fills (which shouldn't happen with slog), the Emit call would block until space is available. No drop policy -- all events are logged.

2. **Two-phase close with closeCh** -- Close signals Emit to stop via closeCh, then closes eventCh to terminate drainLoop, then waits on done. This prevents send-on-closed-channel panics without requiring a mutex on the send path.

3. **Metadata keys prefixed with "meta."** -- Prevents collision with standard event fields (action, actor, target, etc.). Each metadata key-value pair becomes `meta.{key}={value}` in the slog output.

4. **No drop policy** -- All events are logged. If the channel buffer fills, Emit blocks. This is acceptable for the community edition (low volume). Enterprise editions may implement sampling or async persistence.

## Deviations from Plan

- **closeCh added to SlogEmitter struct** -- Not in original design. Required to fix the send-on-closed-channel panic discovered in CI. The original design relied on `<-e.done` winning the select race, which is not guaranteed by Go's select semantics.

## Coverage Report

```
internal/audit  100.0% of statements
```

All functions at 100% coverage. 8 tests: emit JSON, all fields, flush on close, idempotent close, emit after close, concurrent emit (20 goroutines), default buffer size, action constant values.
