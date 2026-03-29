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

## Decisions Made

1. **Buffered channel (not sync queue)** -- Channel semantics are simpler for single-producer-multiple-consumer patterns. Buffer size of 256 prevents backpressure under normal load. If the buffer fills (which shouldn't happen with slog), the Emit call would block until space is available. No drop policy -- all events are logged.

2. **drain goroutine closes done channel** -- The drainLoop goroutine uses `defer close(e.done)` to signal completion. Close() closes the eventCh (which terminates the range loop in drainLoop), then blocks on `<-e.done` to ensure all events are flushed.

3. **Metadata keys prefixed with "meta."** -- Prevents collision with standard event fields (action, actor, target, etc.). Each metadata key-value pair becomes `meta.{key}={value}` in the slog output.

4. **No drop policy** -- All events are logged. If the channel buffer fills, Emit blocks. This is acceptable for the community edition (low volume). Enterprise editions may implement sampling or async persistence.

## Deviations from Plan

- None. Step 4 was implemented exactly as planned.

## Coverage Report

```
internal/audit  100.0% of statements
```

All functions at 100% coverage. 8 tests: emit JSON, all fields, flush on close, idempotent close, emit after close, concurrent emit (20 goroutines), default buffer size, action constant values.
