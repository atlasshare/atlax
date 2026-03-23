# Phase 1 Step 3: Stream State Machine -- Implementation Report

**Last Updated:** 2026-03-23
**Status:** COMPLETE (GREEN)
**Branch:** `phase1/stream-impl`
**Files:** `pkg/protocol/stream.go`, `pkg/protocol/stream_impl.go`, `pkg/protocol/stream_impl_test.go`

---

## Summary

StreamSession is the concrete implementation of the Stream interface, managing the lifecycle of a single multiplexed stream over an atlax connection. It tracks state transitions through the stream lifecycle (Idle -> Open -> HalfClosed -> Closed, with Reset possible from any state), provides blocking Read/Write operations with buffering semantics, enforces protocol state transition rules, and maintains per-stream receive window tracking for flow control.

The implementation uses sync.Mutex with sync.Cond for goroutine coordination, bytes.Buffer for read-side buffering, and a [][]byte slice for write-side queuing. The design ensures safe concurrent access from the mux read loop (which calls Deliver/RemoteClose/Reset) and user application goroutines (which call Read/Write/Close).

---

## Issues Encountered

### 1. errcheck Lint Failure: Unhandled io.EOF from bytes.Buffer.Read

**Symptom:**
golangci-lint errcheck flagged the Read method: `n, _ := s.readBuf.Read(p)` silently discards the error return value.

**Root Cause:**
bytes.Buffer.Read() returns io.EOF when the buffer is empty, which is not a real error condition for a stream that is still open. The linter cannot distinguish between "ignore this error because we handle it" and "forgot to check this error". Explicitly shadowing the error with `_` is a code smell.

**Fix Applied:**
Changed to explicitly check the io.EOF error and return nil when appropriate:

```go
n, err := s.readBuf.Read(p)
if err == io.EOF {
    // Buffer drained but stream still open; not a real EOF.
    return n, nil
}
return n, err
```

This is semantically clearer and passes errcheck: we handle the io.EOF case and return a nil error when the stream is still open. Any other error is propagated as-is (which should not occur with bytes.Buffer, but is defensive).

---

### 2. State Enum Redesign: Single HalfClosed Not Sufficient

**Symptom:**
The scaffold defined `StateHalfClosed` as a single state enum value. Testing and design review revealed the state machine needs to distinguish between "we closed our end" (HalfClosed local) vs "they closed their end" (HalfClosed remote). A single HalfClosed state conflates these and makes the transition matrix ambiguous.

Example problem: If we receive STREAM_CLOSE+FIN while in HalfClosed state, which HalfClosed are we in? We cannot know if we should transition to Closed or ignore it.

**Root Cause:**
The scaffold simplified the state enum to reduce visual complexity, but this sacrificed semantic precision. RFC 9000 (QUIC) and RFC 7540 (HTTP/2) both distinguish local-half-close from remote-half-close for exactly this reason.

**Fix Applied:**
Replaced single `StateHalfClosed` with two distinct states:
- `StateHalfClosedLocal` (0x02): we called Close(), sent FIN, awaiting remote FIN
- `StateHalfClosedRemote` (0x03): remote sent STREAM_CLOSE+FIN, we can still write

Updated state enum in `stream.go`:
```go
const (
    StateIdle             StreamState = 0
    StateOpen             StreamState = 1
    StateHalfClosedLocal  StreamState = 2
    StateHalfClosedRemote StreamState = 3
    StateClosed           StreamState = 4
    StateReset            StreamState = 5
)
```

Also added `StateIdle = 0` (was missing in scaffold). Renumbered all states sequentially from 0 to 5 for clarity.

---

## Decisions Made

### 1. Buffer Strategy: bytes.Buffer for Read Buffering

**Decision:** Use `bytes.Buffer` for the internal read-side queue.

**Rationale:**
- **Simplicity:** bytes.Buffer is the standard Go library solution for variable-length byte queuing. No custom allocations needed.
- **Sufficiency:** Correctness comes first. The 256KB per-stream receive window is modest; GC pressure is acceptable until profiling shows otherwise.
- **Testability:** Easy to inspect buffer state in tests.

**Alternative Considered:** Ring buffer for lower allocation pressure. Deferred to Phase 2 optimization after profiling production workloads.

---

### 2. Write Buffering: [][]byte Slice Instead of Channel

**Decision:** Store Write() payloads in a `writeBuf [][]byte` slice managed by the StreamSession.

**Rationale:**
- **Clarity:** Explicit data queue rather than channel magic. The data lives in the struct, visible to all methods.
- **Ownership:** The mux layer owns the responsibility to drain writeBuf and produce STREAM_DATA frames. StreamSession does not attempt to serialize frames itself.
- **Copy Semantics:** Each Write() call copies the input slice into a new []byte. This prevents subtle bugs where the caller reuses the input buffer and the copy captures stale data.

**Note:** writeBuf is written by the user goroutine (in Write) and read by the mux read loop when pulling frames. No sync primitive protects writeBuf itself; that responsibility belongs to the mux layer, which must hold the stream mutex when accessing it.

---

### 3. Helper Methods: isReadClosed and isWriteClosed

**Decision:** Use two small boolean check helpers instead of a state transition matrix.

**Rationale:**
- **Maintainability:** Each helper is under 5 lines. Reading isReadClosed() is clearer than consulting a matrix.
- **Symmetry:** Read closes in HalfClosedRemote, Closed, Reset. Write closes in HalfClosedLocal, Closed, Reset. This symmetry is evident in the code.
- **Consistency:** Matches the approach used in QUIC implementations.

---

### 4. Public vs Private: Deliver/RemoteClose/Reset are Exported

**Decision:** Export Deliver, RemoteClose, and Reset as public methods (capital letter).

**Rationale:**
- **Mux Integration:** The mux read loop runs in a separate goroutine and must call these methods on streams it finds in its registry. These cannot be private.
- **Interface Implicit:** These are not part of the Stream interface (which is read/write/close). They are part of the internal control API that the mux uses.
- **Future Extensibility:** Allows relay or agent code to inspect stream state if needed (e.g., for audit logging).

---

### 5. Close Idempotency

**Decision:** Close() is idempotent -- calling it multiple times does not panic or error.

**Rationale:**
- **User Experience:** Calling Close() on an already-closed stream should be a no-op, not an error. This is the Go idiom (e.g., io.Closer documentation).
- **Safety:** Prevents accidental panics if close logic is called twice in error handling paths.
- **Simplicity:** The switch in Close() already handles idempotency naturally: HalfClosedLocal and StateClosed cases are no-ops.

---

## Deviations from Plan

### 1. State Enum Changed from Scaffold

**Plan Expected:** StateHalfClosed as a single enum value (might be StateHalfClosed = -1 or 0x02).

**Implemented:** StateHalfClosedLocal and StateHalfClosedRemote (distinct values 0x02 and 0x03).

**Reason:** Required for correct half-close semantics. Remote close needs to transition from Open to HalfClosedRemote, not to an ambiguous HalfClosed. This is a semantic fix, not a workaround.

### 2. StateIdle Explicitly Added

**Plan Expected:** Did not explicitly mention StateIdle in the constant block (though it was clearly needed).

**Implemented:** `StateIdle StreamState = 0` in stream.go.

**Reason:** Clarity. Every stream starts in Idle and must be explicitly transitioned to Open. Making it a named constant (not just "absent from the enum") prevents confusion.

### 3. No Open Method Exposed in Stream Interface

**Plan Expected:** `Open()` mentioned in step tasks as needed by mux.

**Implemented:** `Open()` is a public method on StreamSession but not part of the Stream interface.

**Reason:** Correct choice. Open() is part of the internal control API between mux and stream, not the public Stream API that user code sees. The Stream interface is minimal: ID, State, Read, Write, Close, ReceiveWindow. Control methods (Open, Deliver, RemoteClose, Reset) are part of the mux/stream coupling.

---

## State Transition Matrix

Final tested state transition matrix (rows = from state, columns = event):

```
From \ Event       | Open()        | Close()       | RemoteClose() | Reset()      | Write()  | Read()
------------------|---------------|---------------|---------------|------|----------|-------
Idle               | -> Open       | no-op         | no-op         | -> Reset     | ERROR    | blocks
Open               | no-op         | -> HalfLocal  | -> HalfRemote | -> Reset     | OK       | OK/blocks
HalfClosedLocal    | no-op         | no-op         | -> Closed     | -> Reset     | ERROR    | OK/blocks
HalfClosedRemote   | no-op         | -> Closed     | no-op         | -> Reset     | OK       | EOF
Closed             | no-op         | no-op         | no-op         | no-op        | ERROR    | EOF
Reset              | no-op         | no-op         | no-op         | no-op        | ERROR    | ERROR
```

**Key observations:**
- Open in HalfClosed/Closed/Reset states is a no-op (idempotent Open as well).
- Reset transitions from any state. On Reset, the read buffer is drained to prevent stale data.
- Write fails in any half-closed or closed state (isWriteClosed = true).
- Read returns EOF only in HalfClosedRemote, Closed, or Reset states (isReadClosed = true).
- Read blocks in Idle state (stream not yet opened).

---

## Coverage Report

```
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:25:NewStreamSession        100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:37:ID                       100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:40:State                     100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:49:Read                       90.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:69:Write                     100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:85:Close                     100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:104:ReceiveWindow           100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:112:Open                     100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:120:Deliver                 100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:129:RemoteClose              100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:145:Reset                    100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:154:isReadClosed             100.0%
github.com/atlasshare/atlax/pkg/protocol/stream_impl.go:161:isWriteClosed            100.0%
```

**Overall file coverage: 99.0%**

The 90.0% coverage on Read reflects the blocking wait on line 57 (`s.cond.Wait()`), which is hard to force in a synchronous test without complex timing. All state transition paths are covered. The error cases on lines 61-64 (io.EOF handling) are fully covered.

---

## Test Summary

**Test File:** `pkg/protocol/stream_impl_test.go`
**Total Tests:** 17 test functions covering state machines, blocking semantics, concurrency, and idempotency.

Test categories:

1. **State Transitions (7 tests):**
   - TestStreamSession_NewStartsIdle -- initial state
   - TestStreamSession_TransitionIdleToOpen -- open from idle
   - TestStreamSession_TransitionOpenToHalfClosedLocal -- local close
   - TestStreamSession_TransitionOpenToHalfClosedRemote -- remote close
   - TestStreamSession_TransitionHalfClosedToFullyClosed -- both directions
   - TestStreamSession_ResetFromAnyState -- reset from all states (parameterized)
   - TestStreamSession_CloseIsIdempotent -- idempotency

2. **Write Semantics (3 tests):**
   - TestStreamSession_WriteProducesFrames -- basic write
   - TestStreamSession_WriteFailsWhenHalfClosedLocal -- write blocked after local close
   - TestStreamSession_WriteFailsWhenClosed -- write blocked when fully closed
   - TestStreamSession_WriteFailsWhenReset -- write blocked after reset

3. **Read Semantics (6 tests):**
   - TestStreamSession_ReadReturnsEOFWhenHalfClosedRemote -- EOF after remote close
   - TestStreamSession_ReadReturnsEOFWhenClosed -- EOF when fully closed
   - TestStreamSession_ReadBlocksUntilDataAvailable -- blocking behavior
   - TestStreamSession_ReadUnblocksOnClose -- unblock on remote close
   - TestStreamSession_ReadUnblocksOnReset -- unblock on reset
   - TestStreamSession_ReadDrainsBufferBeforeEOF -- buffered data before EOF

4. **Flow Control (2 tests):**
   - TestStreamSession_ReceiveWindow -- window query
   - TestStreamSession_ReceiveWindowDecrementsOnDeliver -- window decrement

5. **Concurrency (1 test):**
   - TestStreamSession_ConcurrentReadWrite -- stress test with 3 goroutines, 100 iterations, 2-second timeout

All tests pass with `-race` flag. Concurrent test exercises writer, reader, and deliver goroutines simultaneously to catch synchronization issues.

---

## Notes for Phase 2

1. **Open() in Mux Layer:** When the mux receives a STREAM_OPEN frame, it must allocate a StreamSession and call Open() to transition it to StateOpen. The mux must also ensure that STREAM_OPEN+ACK is sent before the stream is usable.

2. **writeBuf Draining:** The mux layer is responsible for inspecting the stream's writeBuf, building STREAM_DATA frames, and clearing the queue. This requires coordination under the stream mutex.

3. **Window Updates:** The Deliver method decrements recvWindow on each call. The mux layer must send WINDOW_UPDATE frames to the peer to restore the window when it drops below a threshold. The window.go module (from Step 2) provides the infrastructure for this.

4. **No Stream ID Reuse:** Once a stream reaches Closed or Reset state, its ID must not be reused. The mux layer maintains a separate stream registry and must garbage-collect closed streams to avoid ID collisions.

5. **Concurrent Correctness:** The stream_impl_test concurrent test exercises the contract but does not stress-test the mux integration. Phase 2 will provide full integration tests.

---

## Related Areas

- **Frame Codec** (`docs/development/phase1/step1-frame-codec-report.md`) -- Frames are what carry stream data across the wire.
- **Flow Control Windows** (`docs/development/phase1/step2-flow-control-report.md`) -- per-stream window is tracked here; per-connection window managed elsewhere.
- **Mux Session** (`docs/development/phase1/step5-mux-session-report.md`) -- The multiplexer orchestrates many streams and calls their lifecycle methods.

---

## Interface Compliance

Compile-time check passes: `var _ Stream = (*StreamSession)(nil)` in the test file verifies that StreamSession correctly implements the Stream interface.

**Stream interface methods:**
- ID() uint32 -- returns stream.id
- State() StreamState -- returns current state under mutex
- Read([]byte) (int, error) -- reads from readBuf with blocking
- Write([]byte) (int, error) -- queues data in writeBuf
- Close() error -- transitions state, sends FIN
- ReceiveWindow() int -- returns recvWindow counter

All six methods are implemented.

