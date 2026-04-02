# Step 4 Report: Stream ID Recycling

**Date:** 2026-04-02
**Branch:** `phase5/stream-recycling`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Stream IDs are now recycled after streams close. Previously, IDs incremented monotonically and would overflow after ~2^31 streams (~24 days at max load). Now closed stream IDs are returned to a free list and reused by the next `OpenStream` call.

**Changes:**
- `MuxSession.freeIDs []uint32`: LIFO free list of recycled stream IDs
- `OpenStreamWithPayload`: pops from freeIDs before incrementing nextStreamID
- `maybeRemoveStream`: appends closed stream ID to freeIDs
- `handleStreamReset`: appends reset stream ID to freeIDs
- `removeStream`: appends failed-open stream ID to freeIDs

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| First recycling test failed: got ID 3 instead of 1 | `s1.Close()` only half-closes (HalfClosedLocal). Stream must reach Closed state for ID to be recycled. | Both sides must close. Test updated to close relay side then agent side. |

## Decisions Made

1. **LIFO free list (stack)** -- Simple append/pop on a slice. Most recently freed ID reused first. No sorting needed.
2. **All deletion paths recycle** -- maybeRemoveStream, handleStreamReset, and removeStream all add to freeIDs. No path leaks an ID.
3. **No cap on free list** -- Free list grows up to max concurrent streams, which is bounded by config. Not unbounded.

## Coverage Report

3 new tests: basic recycling, parity preservation, high churn (20 cycles).
