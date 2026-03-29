# Prerequisite: Review Phase 1 Carry-Forward Items

## Why

Phase 1 documented 7 open items that affect Phase 2 implementation. Before starting, confirm which items are in-scope for Phase 2 and which are deferred. This prevents scope creep and ensures the plan accounts for known gaps.

## Items to Review

Read `docs/development/phase1/phase1-completion-report.md` section "Open Items Carried Forward to Phase 2" and confirm each:

### 1. Full STREAM_OPEN handshake

**Phase 1 status:** OpenStream returns immediately, no ACK wait.
**Phase 2 scope:** IN SCOPE. Step 1 (mTLS + protocol fixes) will implement STREAM_OPEN ACK handling in MuxSession.handleFrame.

### 2. FlowWindow integration in MuxSession

**Phase 1 status:** handleWindowUpdate is a stub.
**Phase 2 scope:** IN SCOPE. Step 1 will wire WINDOW_UPDATE frames to window.Update() on the affected stream.

### 3. Stream Write -> STREAM_DATA transport

**Phase 1 status:** StreamSession.Write buffers data but never emits STREAM_DATA frames.
**Phase 2 scope:** IN SCOPE. Step 1 will add a drain mechanism in MuxSession that reads stream write buffers and schedules STREAM_DATA frames through the write queue.

### 4. Stream ID exhaustion

**Phase 1 status:** No recycling of closed stream IDs.
**Phase 2 scope:** DEFERRED to Phase 5 (production hardening). At 1000 concurrent streams with 30s average lifetime, ID exhaustion takes ~24 days of continuous operation. Acceptable for Phase 2.

### 5. sync.Pool for Frame objects

**Phase 1 status:** 4192 B/op on decode.
**Phase 2 scope:** DEFERRED to Phase 5 (production hardening). Optimize after load testing reveals allocation pressure.

### 6. Fuzz testing

**Phase 1 status:** FrameCodec not fuzz-tested.
**Phase 2 scope:** DEFERRED. Will be addressed in a dedicated security phase.

### 7. Error.Error() untested

**Phase 1 status:** 0% coverage on Error type.
**Phase 2 scope:** IN SCOPE. Phase 2 tests will naturally exercise Error when testing protocol violations during mTLS handshake failures and stream error recovery.

## Action

Review each item above. If you disagree with any scoping decision, adjust before starting Phase 2.

**Reviewed and confirmed by Ruben on 2026-03-28.** All scope decisions accepted. Deferred items must be carried forward in the Phase 2 completion report.

## Deferred Items Tracker

These items MUST appear in `docs/development/phase2/phase2-completion-report.md` under "Open Items Carried Forward" and be referenced in subsequent phase plans:

| # | Item | Deferred To | Rationale |
|---|------|-------------|-----------|
| 4 | Stream ID exhaustion / recycling | Phase 5 (production hardening) | ~24 days before overflow at max load; acceptable pre-production |
| 5 | sync.Pool for Frame objects | Phase 5 (production hardening) | Optimize after load testing confirms allocation pressure |
| 6 | Fuzz testing for FrameCodec | Dedicated security phase | Security-specific testing, not blocking functionality |

## Done When

- All 7 items reviewed and scope confirmed
- No surprises expected during implementation
