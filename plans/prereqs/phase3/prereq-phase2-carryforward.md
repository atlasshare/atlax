# Prerequisite: Review Phase 2 Carry-Forward Items

## Why

Phase 2 completed with three high-severity deviations that Phase 3 must resolve. Confirm the step assignments before starting.

## Items

### 1. STREAM_CLOSE wire emission (Phase 3 Step 1)

**Current state:** Stream.Close() only transitions local state. Peer never learns stream closed gracefully. Teardown uses context+Reset (hard abort).
**Phase 3 fix:** Add onClose callback in StreamSession. MuxSession enqueues STREAM_CLOSE+FIN frame when Close() is called. ~20 lines of code.

### 2. Multi-service routing via STREAM_OPEN payload (Phase 3 Step 3)

**Current state:** Agent resolveTarget returns sole configured service. Multi-service agents are broken.
**Phase 3 fix:** Relay includes service name in STREAM_OPEN payload. Agent parses payload in resolveTarget.

### 3. Reconnection supervision (Phase 3 Step 6)

**Current state:** Heartbeat exits on failure. Agent process must be restarted by systemd.
**Phase 3 fix:** Integration test verifies agent reconnects when relay restarts. May add supervision loop in agent Tunnel.Start if needed.

## Action

Review each item. Confirm step assignments are correct. If any should move to a different step, adjust the plan before starting.

## Done When

- All 3 items reviewed and step assignments confirmed
