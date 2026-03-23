# Blueprint: Phase 1 -- Core Protocol Implementation

**Objective:** Implement the atlax wire protocol multiplexing library in `pkg/protocol/` -- the foundational layer upon which all tunnel communication is built.

**Status:** ALL STEPS COMPLETE
**Target duration:** 2 weeks
**Estimated sessions:** 3-5 (steps 1-2 serial, steps 3-5 parallelizable, step 6 serial)

**Prerequisites:** Scaffold complete (Phase 0). All prerequisite tasks in `plans/prereqs/` must be completed by the user before execution begins.

---

## Dependency Graph

```
Step 1 (Frame Codec)
   |
   v
Step 2 (Flow Control Windows)
   |
   +--> Step 3 (Stream State Machine)  --+
   |                                      |
   +--> Step 4 (UDP Framing)             +--> Step 6 (Integration & Ship)
   |                                      |
   +--> Step 5 (Multiplexer Session)  ---+
```

**Parallelism:** Steps 3, 4, 5 depend on Steps 1-2 but share no files and can execute concurrently.

---

## Naming Conventions (enforced across ALL steps)

- **New file naming:** Implementation files use descriptive suffixes (`_codec`, `_impl`, `_session`). Test files use `_test` suffix.
- **Type naming:** Concrete types are named directly (e.g., `FrameCodec`, `StreamSession`, `MuxSession`). No "Impl" suffix.
- **Error wrapping:** All errors use `fmt.Errorf("component: operation: %w", err)` pattern.
- **Constants:** Grouped by concern (commands, flags, defaults, limits) in separate `const` blocks.

---

## Invariants (verified after EVERY step)

1. `go build ./...` passes
2. `go vet ./...` passes
3. `go test -race ./pkg/protocol/...` passes
4. `gofmt -l .` returns no output
5. `golangci-lint run ./pkg/protocol/...` passes
6. Coverage for `pkg/protocol/` is >= 90%
7. No function exceeds 50 lines
8. No file exceeds 800 lines
9. No hardcoded secrets or private keys
10. No emoji in code, comments, or test output
11. All new types satisfy existing interfaces where applicable

---

## Step 1: Frame Codec -- TDD

**Branch:** `phase1/frame-codec`
**Depends on:** Phase 0 (scaffold)
**Model tier:** strongest (binary protocol correctness is critical)
**Serial:** yes (all other steps depend on frame encoding)
**Rollback:** `git branch -D phase1/frame-codec`

### Context Brief

Implement the binary frame encoder and decoder for the atlax wire protocol. The codec must serialize/deserialize the 12-byte header (Version, Command, Flags, Reserved, StreamID, PayloadLength) in big-endian byte order, followed by the variable-length payload. The existing `Frame`, `FrameReader`, and `FrameWriter` types in `frame.go` define the contract.

**Wire format reference:** `docs/protocol/wire-format.md` -- contains exact byte offsets, endianness, validation rules, and hex dump examples.

**Critical rules:**
- Reject frames with unknown protocol versions
- Reject frames with payload length > MaxPayloadSize (16MB)
- Reserved byte must be 0x00 on encode, ignored on decode
- Connection-level frames (StreamID 0) must not carry STREAM_OPEN/DATA/CLOSE/RESET

### Tasks

#### Test file: `pkg/protocol/frame_codec_test.go`

Write tests FIRST (TDD RED phase):

- [ ] Test encode/decode round-trip for each command (0x01-0x0B)
- [ ] Test encode/decode with all flag combinations (0x00, 0x01, 0x02, 0x03)
- [ ] Test encode/decode with zero-length payload
- [ ] Test encode/decode with maximum payload size (16MB boundary)
- [ ] Test encode/decode with various stream IDs (0, 1, 2, MaxUint32)
- [ ] Test big-endian byte ordering of StreamID and PayloadLength
- [ ] Test rejection of payload exceeding MaxPayloadSize
- [ ] Test rejection of unknown protocol version
- [ ] Test encode sets Reserved byte to 0x00
- [ ] Test decode ignores Reserved byte value
- [ ] Test reading from a truncated source (partial header, partial payload)
- [ ] Test reading from an io.Reader that returns data in small chunks
- [ ] Test wire examples from docs/protocol/wire-format.md (PING, STREAM_OPEN, STREAM_DATA+FIN, WINDOW_UPDATE)
- [ ] Benchmark: BenchmarkEncodeFrame, BenchmarkDecodeFrame

#### Implementation file: `pkg/protocol/frame_codec.go`

Write implementation SECOND (TDD GREEN phase):

- [ ] `type FrameCodec struct{}` -- satisfies both FrameReader and FrameWriter interfaces
- [ ] `func NewFrameCodec() *FrameCodec`
- [ ] `func (c *FrameCodec) ReadFrame(r io.Reader) (*Frame, error)` -- read 12-byte header, validate, read payload
- [ ] `func (c *FrameCodec) WriteFrame(w io.Writer, f *Frame) error` -- encode header, write header + payload
- [ ] `func (c *FrameCodec) validateHeader(hdr [HeaderSize]byte) error` -- version check, payload size check
- [ ] Add `String()` method to `Command` type in `frame.go` for debugging
- [ ] Add `String()` method to `Flag` type in `frame.go` for debugging
- [ ] Add `IsValid()` method to `Command` type in `frame.go`

### Verification

```bash
go test -race -v -coverprofile=coverage.out ./pkg/protocol/...
go tool cover -func=coverage.out | grep frame_codec
# coverage for frame_codec.go must be >= 90%
go vet ./pkg/protocol/...
golangci-lint run ./pkg/protocol/...
```

### Post-Green Documentation

After all tests pass and CI is green, write a step report in `docs/development/phase1/step1-frame-codec-report.md`:

- [ ] **Summary:** What was implemented and why (frame codec purpose in the protocol stack)
- [ ] **Issues encountered:** Every bug, compiler error, linter failure, and test failure hit during development. Include the symptom, root cause, and fix applied. If a test was wrong, explain why.
- [ ] **Decisions made:** Any design choices not already in the plan (e.g., buffer strategy, error message format, helper decomposition). Record rationale.
- [ ] **Deviations from plan:** Anything that differed from the tasks above -- added tests, removed tasks, changed type signatures. Explain why.
- [ ] **Bead tree updates:** Update relevant bead files (`bead-tree/protocol/frame-concerns.md`, `bead-tree/decisions-log.md`, `bead-tree/open-questions.md`) with anything learned. Close resolved items.
- [ ] **Coverage report:** Paste `go tool cover -func` output for frame_codec.go
- [ ] **Benchmark results:** Paste BenchmarkEncodeFrame and BenchmarkDecodeFrame output

### Exit Criteria

- All frame encode/decode tests pass with -race
- Wire example tests match exact hex from docs
- Coverage >= 90% for frame_codec.go
- FrameCodec satisfies FrameReader and FrameWriter interfaces (compile-time check)
- No function exceeds 50 lines
- Step report written in `docs/development/phase1/step1-frame-codec-report.md`
- PR: `phase1/frame-codec` -> `main`

---

## Step 2: Flow Control Windows

**Branch:** `phase1/flow-control`
**Depends on:** Step 1
**Model tier:** strongest (flow control correctness prevents deadlocks)
**Serial:** yes (stream and mux depend on this)
**Rollback:** `git branch -D phase1/flow-control`

### Context Brief

Implement per-stream and connection-level flow control window tracking. The window module tracks available send capacity, decrements on data send, increments on WINDOW_UPDATE receipt, and blocks senders when exhausted. This is a standalone module with no I/O -- it tracks numeric state only.

**Flow control reference:** `docs/protocol/flow-control.md` -- contains defaults, increment rules, overflow protection, deadlock prevention strategy.

**Critical rules:**
- Per-stream default: 262,144 bytes (256KB)
- Connection-level default: 1,048,576 bytes (1MB)
- WINDOW_UPDATE increment must be > 0 (zero increment is protocol error)
- Window must not exceed 2^31 - 1 after increment (overflow is protocol error)
- Blocking must be interruptible via context cancellation

### Tasks

#### Test file: `pkg/protocol/window_test.go`

Write tests FIRST:

- [ ] Test new window with default size
- [ ] Test new window with custom size
- [ ] Test consume reduces available capacity
- [ ] Test consume blocks when window exhausted (use goroutine + channel)
- [ ] Test consume unblocks when window is updated
- [ ] Test consume respects context cancellation while blocked
- [ ] Test update increments available capacity
- [ ] Test update rejects zero increment (returns protocol error)
- [ ] Test update rejects overflow past 2^31-1 (returns protocol error)
- [ ] Test concurrent consume and update (goroutine safety with -race)
- [ ] Test window size never goes negative
- [ ] Test Available() returns correct remaining capacity
- [ ] Benchmark: BenchmarkWindowConsume, BenchmarkWindowUpdate

#### Implementation file: `pkg/protocol/window.go`

Write implementation SECOND:

- [ ] `type FlowWindow struct` -- available int32, mu sync.Mutex, cond sync.Cond
- [ ] `func NewFlowWindow(initialSize int32) *FlowWindow`
- [ ] `func (w *FlowWindow) Consume(ctx context.Context, n int32) error` -- block if insufficient, respect context
- [ ] `func (w *FlowWindow) Update(increment int32) error` -- validate > 0, check overflow, signal waiters
- [ ] `func (w *FlowWindow) Available() int32` -- current remaining capacity
- [ ] `func (w *FlowWindow) Reset()` -- reset to initial size (for stream reset scenarios)
- [ ] Add sentinel errors to errors.go: `ErrZeroWindowIncrement`, `ErrWindowOverflow`

### Verification

```bash
go test -race -v -count=5 ./pkg/protocol/... -run TestWindow
go test -race -coverprofile=coverage.out ./pkg/protocol/...
go tool cover -func=coverage.out | grep window
```

### Post-Green Documentation

After all tests pass and CI is green, write a step report in `docs/development/phase1/step2-flow-control-report.md`:

- [ ] **Summary:** What was implemented and why (flow control's role in preventing sender overload and deadlock)
- [ ] **Issues encountered:** Every bug, race condition, deadlock scenario, linter failure, and test failure hit during development. Include symptom, root cause, and fix.
- [ ] **Decisions made:** Any design choices about blocking strategy, sync primitive selection (Mutex vs RWMutex, Cond vs channel), or context integration. Record rationale.
- [ ] **Deviations from plan:** Anything that differed from the tasks above. Explain why.
- [ ] **Bead tree updates:** Update `bead-tree/protocol/stream-concerns.md`, `bead-tree/decisions-log.md`, `bead-tree/risk-register.md` (especially R-001 deadlock risk). Close resolved items.
- [ ] **Coverage report:** Paste `go tool cover -func` output for window.go
- [ ] **Benchmark results:** Paste BenchmarkWindowConsume and BenchmarkWindowUpdate output

### Exit Criteria

- All window tests pass with -race (run 5 times to catch races)
- No deadlock possible (context cancellation always unblocks)
- Overflow detection works at 2^31-1 boundary
- Coverage >= 90% for window.go
- Step report written in `docs/development/phase1/step2-flow-control-report.md`
- PR: `phase1/flow-control` -> `main`

---

## Step 3: Stream State Machine

**Branch:** `phase1/stream-impl`
**Depends on:** Steps 1, 2
**Model tier:** strongest (state machine correctness is critical)
**Parallel with:** Steps 4, 5
**Rollback:** `git branch -D phase1/stream-impl`

### Context Brief

Implement the concrete Stream type that manages the lifecycle of a single multiplexed stream. The stream tracks its state (Idle -> Open -> HalfClosed -> Closed, or Reset from any state), provides Read/Write with flow control, and enforces state transition rules.

**State machine reference:** `docs/protocol/stream-lifecycle.md` -- contains state diagram, transition rules, half-closed semantics, error codes.

**Critical rules:**
- Write must fail in HalfClosed(local) or Closed state
- Read must fail in HalfClosed(remote) or Closed state
- STREAM_RESET transitions from any state to Reset
- Stream ID is never reused once Closed or Reset
- Read/Write must respect flow control windows

### Tasks

#### Test file: `pkg/protocol/stream_impl_test.go`

Write tests FIRST:

- [ ] Test new stream starts in Idle state
- [ ] Test stream transitions: Idle -> Open (on ACK)
- [ ] Test stream transitions: Open -> HalfClosed(local) (on local close)
- [ ] Test stream transitions: Open -> HalfClosed(remote) (on remote close)
- [ ] Test stream transitions: HalfClosed -> Closed (both sides closed)
- [ ] Test stream transitions: any state -> Reset
- [ ] Test Write fails with ErrStreamClosed in HalfClosed(local)
- [ ] Test Write fails with ErrStreamClosed in Closed state
- [ ] Test Read returns io.EOF in HalfClosed(remote)
- [ ] Test Read returns io.EOF in Closed state
- [ ] Test Read blocks until data available
- [ ] Test Read unblocks on close (returns io.EOF)
- [ ] Test Write respects per-stream flow control window
- [ ] Test ReceiveWindow returns correct value
- [ ] Test ID returns the stream identifier
- [ ] Test concurrent Read/Write from separate goroutines
- [ ] Test Close is idempotent (calling twice does not panic)

#### Implementation file: `pkg/protocol/stream_impl.go`

Write implementation SECOND:

- [ ] `type StreamSession struct` -- id, state, readBuf, writeCh, recvWindow, sendWindow, mu, closedCh
- [ ] `func NewStreamSession(id uint32, config StreamConfig) *StreamSession`
- [ ] `func (s *StreamSession) ID() uint32`
- [ ] `func (s *StreamSession) State() StreamState`
- [ ] `func (s *StreamSession) Read(p []byte) (int, error)` -- read from internal buffer, block if empty
- [ ] `func (s *StreamSession) Write(p []byte) (int, error)` -- check state, consume send window, queue frame
- [ ] `func (s *StreamSession) Close() error` -- transition to HalfClosed/Closed
- [ ] `func (s *StreamSession) ReceiveWindow() int`
- [ ] `func (s *StreamSession) deliver(data []byte)` -- called by muxer to deliver incoming data
- [ ] `func (s *StreamSession) reset(code uint32)` -- called by muxer on STREAM_RESET
- [ ] `func (s *StreamSession) remoteClose()` -- called by muxer on remote STREAM_CLOSE+FIN
- [ ] Add `StateIdle StreamState = -1` to stream.go (or use existing states; adjust as needed)
- [ ] Compile-time interface check: `var _ Stream = (*StreamSession)(nil)`

### Verification

```bash
go test -race -v -count=3 ./pkg/protocol/... -run TestStream
go test -race -coverprofile=coverage.out ./pkg/protocol/...
go tool cover -func=coverage.out | grep stream_impl
```

### Post-Green Documentation

After all tests pass and CI is green, write a step report in `docs/development/phase1/step3-stream-impl-report.md`:

- [ ] **Summary:** What was implemented and why (stream as the unit of multiplexed communication)
- [ ] **Issues encountered:** Every bug, state machine edge case, race condition, and test failure hit during development. Include symptom, root cause, and fix.
- [ ] **Decisions made:** Buffer strategy chosen (ring buffer vs bytes.Buffer vs channel), half-closed representation (enum vs booleans), deliver/reset/remoteClose API design. Record rationale.
- [ ] **Deviations from plan:** Any state transitions added or removed vs the plan. Any changes to the Stream interface or StreamConfig. Explain why.
- [ ] **Bead tree updates:** Update `bead-tree/protocol/stream-concerns.md` (close resolved design decisions), `bead-tree/decisions-log.md`, `bead-tree/risk-register.md` (R-002 race conditions). Close resolved items.
- [ ] **State transition matrix:** Include the final tested state transition matrix showing all valid and invalid transitions.
- [ ] **Coverage report:** Paste `go tool cover -func` output for stream_impl.go

### Exit Criteria

- All state transition tests pass
- Concurrent Read/Write tests pass with -race (run 3 times)
- StreamSession satisfies Stream interface (compile-time check)
- Coverage >= 90% for stream_impl.go
- No function exceeds 50 lines
- Step report written in `docs/development/phase1/step3-stream-impl-report.md`
- PR: `phase1/stream-impl` -> `main`

---

## Step 4: UDP Framing

**Branch:** `phase1/udp-framing`
**Depends on:** Step 1
**Model tier:** default
**Parallel with:** Steps 3, 5
**Rollback:** `git branch -D phase1/udp-framing`

### Context Brief

Implement encoding and decoding for the UDP tunneling frames: UDP_BIND, UDP_DATA, and UDP_UNBIND. The UDP_DATA payload has a nested format: addr_length(1B) + source_addr(variable) + udp_payload. This step extends the FrameCodec with UDP-specific payload parsing.

**UDP reference:** `docs/protocol/udp-tunneling.md` -- contains payload format, limitations, use cases.

### Tasks

#### Test file: `pkg/protocol/udp_test.go`

Write tests FIRST:

- [ ] Test encode/decode UDP_BIND frame with bind address
- [ ] Test encode/decode UDP_UNBIND frame with bind address
- [ ] Test encode/decode UDP_DATA frame with source addr + payload
- [ ] Test UDP_DATA with maximum address length (255 bytes)
- [ ] Test UDP_DATA with empty UDP payload (addr only)
- [ ] Test UDP_DATA round-trip preserves source address and payload exactly
- [ ] Test ParseUDPDataPayload extracts addr and data correctly
- [ ] Test BuildUDPDataPayload constructs correct binary format
- [ ] Test invalid UDP_DATA payload (truncated addr, addr_length > remaining bytes)

#### Implementation file: `pkg/protocol/udp.go`

Write implementation SECOND:

- [ ] `type UDPDatagram struct` -- SourceAddr string, Payload []byte
- [ ] `func ParseUDPDataPayload(payload []byte) (*UDPDatagram, error)` -- parse addr_length + addr + data
- [ ] `func BuildUDPDataPayload(sourceAddr string, data []byte) ([]byte, error)` -- build binary payload
- [ ] `func NewUDPBindFrame(bindAddr string) *Frame` -- convenience constructor
- [ ] `func NewUDPUnbindFrame(bindAddr string) *Frame` -- convenience constructor
- [ ] `func NewUDPDataFrame(streamID uint32, sourceAddr string, data []byte) (*Frame, error)` -- convenience constructor
- [ ] Add sentinel errors: `ErrInvalidUDPPayload`, `ErrUDPAddrTooLong`

### Verification

```bash
go test -race -v ./pkg/protocol/... -run TestUDP
go test -race -coverprofile=coverage.out ./pkg/protocol/...
go tool cover -func=coverage.out | grep udp
```

### Post-Green Documentation

After all tests pass and CI is green, write a step report in `docs/development/phase1/step4-udp-framing-report.md`:

- [ ] **Summary:** What was implemented and why (UDP-over-TCP tunneling for WireGuard, DNS use cases)
- [ ] **Issues encountered:** Every bug, encoding edge case, linter failure, and test failure hit during development. Include symptom, root cause, and fix.
- [ ] **Decisions made:** Any choices about payload validation strictness, error handling for malformed datagrams, or convenience constructor API. Record rationale.
- [ ] **Deviations from plan:** Any changes to the UDPDatagram struct, added/removed functions. Explain why.
- [ ] **Bead tree updates:** Update `bead-tree/decisions-log.md` with any new decisions. Close resolved items in `bead-tree/open-questions.md`.
- [ ] **Coverage report:** Paste `go tool cover -func` output for udp.go

### Exit Criteria

- All UDP encode/decode tests pass with -race
- UDP_DATA payload format matches docs spec exactly
- Coverage >= 90% for udp.go
- Step report written in `docs/development/phase1/step4-udp-framing-report.md`
- PR: `phase1/udp-framing` -> `main`

---

## Step 5: Multiplexer Session

**Branch:** `phase1/mux-session`
**Depends on:** Steps 1, 2, 3
**Model tier:** strongest (concurrency-heavy, correctness critical)
**Parallel with:** Steps 3 (after merge), 4
**Rollback:** `git branch -D phase1/mux-session`

### Context Brief

Implement the concrete Muxer that multiplexes streams over a single `io.ReadWriteCloser` (which in production is the TLS connection). The muxer manages stream lifecycle (open/accept/close), stream ID allocation (odd/even), ping/pong heartbeat, GOAWAY, and concurrent stream limits.

**Concurrency model:** The muxer runs a read loop goroutine that dispatches incoming frames to the correct stream. Outgoing frames are serialized through a write queue with priority for control frames (WINDOW_UPDATE, PING, PONG, GOAWAY) over data frames, preventing deadlock per `docs/protocol/flow-control.md`.

### Tasks

#### Test file: `pkg/protocol/mux_session_test.go`

Write tests FIRST:

- [ ] Test create muxer with config
- [ ] Test OpenStream allocates correct stream IDs (relay=odd starting at 1, agent=even starting at 2)
- [ ] Test OpenStream increments stream IDs monotonically
- [ ] Test OpenStream fails when max concurrent streams exceeded
- [ ] Test AcceptStream receives streams opened by remote peer
- [ ] Test AcceptStream blocks until a stream is available
- [ ] Test AcceptStream respects context cancellation
- [ ] Test Close tears down all streams
- [ ] Test Close causes AcceptStream to return error
- [ ] Test GoAway sends GOAWAY frame, rejects new OpenStream, allows existing streams
- [ ] Test Ping/Pong round-trip returns latency duration
- [ ] Test Ping timeout returns error
- [ ] Test NumStreams returns correct count as streams open/close
- [ ] Test control frames (WINDOW_UPDATE, PING, PONG) bypass data flow control
- [ ] Test bidirectional data transfer through muxer pair (pipe-connected)
- [ ] Test concurrent OpenStream from multiple goroutines
- [ ] Integration: pipe two muxers together, open streams, transfer data, close gracefully

#### Implementation file: `pkg/protocol/mux_session.go`

Write implementation SECOND:

- [ ] `type MuxSession struct` -- transport, codec, config, streams map, nextStreamID, acceptCh, closeCh, mu
- [ ] `type MuxRole int` -- `const (RoleRelay MuxRole = iota; RoleAgent)` -- determines odd/even stream ID allocation
- [ ] `func NewMuxSession(transport io.ReadWriteCloser, role MuxRole, config MuxConfig) *MuxSession`
- [ ] `func (m *MuxSession) OpenStream(ctx context.Context) (Stream, error)` -- allocate ID, send STREAM_OPEN, wait for ACK
- [ ] `func (m *MuxSession) AcceptStream(ctx context.Context) (Stream, error)` -- receive from acceptCh
- [ ] `func (m *MuxSession) Close() error` -- send GOAWAY, drain streams, close transport
- [ ] `func (m *MuxSession) GoAway(code uint32) error` -- send GOAWAY frame
- [ ] `func (m *MuxSession) Ping(ctx context.Context) (time.Duration, error)` -- send PING, wait for PONG
- [ ] `func (m *MuxSession) NumStreams() int`
- [ ] Internal: `func (m *MuxSession) readLoop()` -- goroutine: read frames, dispatch to streams or control handlers
- [ ] Internal: `func (m *MuxSession) writeLoop()` -- goroutine: priority queue, control frames first
- [ ] Internal: `func (m *MuxSession) handleFrame(f *Frame)` -- dispatch by command type
- [ ] Compile-time interface check: `var _ Muxer = (*MuxSession)(nil)`

#### Helper file: `pkg/protocol/write_queue.go`

- [ ] `type WriteQueue struct` -- priority queue for outgoing frames (control > data)
- [ ] `func NewWriteQueue() *WriteQueue`
- [ ] `func (q *WriteQueue) Enqueue(f *Frame, priority int)`
- [ ] `func (q *WriteQueue) Dequeue(ctx context.Context) (*Frame, error)` -- blocks until frame available

### Verification

```bash
go test -race -v -count=5 ./pkg/protocol/... -run TestMux
go test -race -coverprofile=coverage.out ./pkg/protocol/...
go tool cover -func=coverage.out | grep -E "mux_session|write_queue"
```

### Post-Green Documentation

After all tests pass and CI is green, write a step report in `docs/development/phase1/step5-mux-session-report.md`:

- [ ] **Summary:** What was implemented and why (multiplexer as the integration layer connecting frames, streams, and flow control)
- [ ] **Issues encountered:** Every bug, race condition, goroutine leak, deadlock scenario, linter failure, and test failure hit during development. Include symptom, root cause, and fix. This is the most complex step -- expect the most issues here.
- [ ] **Decisions made:** Write queue priority design, read/write loop goroutine lifecycle, shutdown sequence, GOAWAY drain timeout value, ping/pong implementation details. Record rationale.
- [ ] **Deviations from plan:** Any changes to MuxSession API, added/removed internal methods, WriteQueue design changes. Explain why.
- [ ] **Bead tree updates:** Update `bead-tree/protocol/mux-concerns.md` (close resolved design items), `bead-tree/decisions-log.md`, `bead-tree/risk-register.md` (R-001 deadlock, R-002 races, R-004 GC pressure). Close resolved items.
- [ ] **Concurrency architecture diagram:** Include the final goroutine architecture (which goroutines exist, what channels/mutexes connect them).
- [ ] **Coverage report:** Paste `go tool cover -func` output for mux_session.go and write_queue.go
- [ ] **Benchmark results:** If any benchmarks were added, paste output.

### Exit Criteria

- All mux tests pass with -race (run 5 times to catch race conditions)
- Pipe-connected integration test demonstrates full lifecycle
- MuxSession satisfies Muxer interface (compile-time check)
- Control frames always bypass data flow control
- Coverage >= 90% for mux_session.go and write_queue.go
- No function exceeds 50 lines
- Step report written in `docs/development/phase1/step5-mux-session-report.md`
- PR: `phase1/mux-session` -> `main`

---

## Step 6: Integration Verification and Ship

**Branch:** `main` (all branches merged)
**Depends on:** Steps 3, 4, 5
**Model tier:** default
**Serial:** yes (final gate)
**Rollback:** N/A (verification only)

### Context Brief

Final verification after all Phase 1 PRs are merged. Run full test suite, verify coverage, ensure all interfaces are satisfied, and confirm the protocol library is ready for Phase 2 (Agent implementation).

### Merge Strategy

Merge branches in dependency order:
1. **Step 1** (`phase1/frame-codec`) -- Foundation
2. **Step 2** (`phase1/flow-control`) -- Depends on Step 1
3. **Step 3** (`phase1/stream-impl`) -- Depends on Steps 1, 2
4. **Step 4** (`phase1/udp-framing`) -- Depends on Step 1
5. **Step 5** (`phase1/mux-session`) -- Depends on Steps 1, 2, 3

### Tasks

- [ ] Merge all five PRs in order specified above
- [ ] Run full test suite: `go test -race -coverprofile=coverage.out ./pkg/protocol/...`
- [ ] Verify overall pkg/protocol coverage >= 90%
- [ ] Run `go vet ./...` -- no warnings
- [ ] Run `golangci-lint run ./...` -- no errors
- [ ] Run `gofmt -l .` -- no unformatted files
- [ ] Verify no function exceeds 50 lines: `grep -c "^func" pkg/protocol/*.go`
- [ ] Verify no file exceeds 800 lines: `wc -l pkg/protocol/*.go`
- [ ] Verify all interfaces satisfied:
  - `FrameCodec` satisfies `FrameReader` and `FrameWriter`
  - `StreamSession` satisfies `Stream`
  - `MuxSession` satisfies `Muxer`
- [ ] Verify no hardcoded secrets or private keys
- [ ] Verify no emoji in any file
- [ ] Run integration test: two MuxSessions connected via pipe, open/close streams, transfer data
- [ ] Update CLAUDE.md if any conventions changed during implementation
- [ ] Tag or note: "Phase 1 complete, ready for Phase 2 (Agent)"

### Post-Green Documentation

After all tests pass and CI is green across the merged codebase, write the Phase 1 completion report in `docs/development/phase1/phase1-completion-report.md`:

- [ ] **Phase summary:** What Phase 1 delivered (mux library with TCP + UDP framing), total files added, total test count, overall coverage
- [ ] **Consolidated issue log:** Aggregate all issues from step reports into a single table: step, issue, root cause, fix, lesson learned
- [ ] **Consolidated decision log:** Aggregate all decisions from step reports. Cross-reference with `bead-tree/decisions-log.md` to ensure nothing was missed.
- [ ] **Open items carried forward:** Any open questions, risks, or concerns that Phase 2 must address. Update `bead-tree/open-questions.md` and `bead-tree/risk-register.md`.
- [ ] **Architecture snapshot:** Current state of `pkg/protocol/` -- file list, type list, interface satisfaction map, dependency graph between files
- [ ] **Performance baseline:** Consolidated benchmark results from all steps. These become the regression baseline for future phases.
- [ ] **CI verification:** Screenshot or paste of all CI jobs green on main after final merge
- [ ] **Bead tree final update:** Close all Phase 1 items in bead tree. Update `bead-tree/architecture/interface-contracts.md` with completed implementations.
- [ ] **Lessons learned:** What went well, what was harder than expected, what should change in the Phase 2 plan based on Phase 1 experience

### Verification

```bash
cd /Users/rubenyomenou/projects/atlax
go test -race -coverprofile=coverage.out ./pkg/protocol/...
go tool cover -func=coverage.out
go vet ./...
golangci-lint run ./...
test -z "$(gofmt -l .)"
# Check file sizes
wc -l pkg/protocol/*.go | sort -n
# Check function count per file
for f in pkg/protocol/*.go; do echo "$f: $(grep -c '^func' $f) functions"; done
```

### Exit Criteria

- All tests pass with -race
- pkg/protocol coverage >= 90%
- All linters pass
- All interfaces compile-time verified
- No file exceeds 800 lines, no function exceeds 50 lines
- All 5 step reports written in `docs/development/phase1/`
- Phase 1 completion report written in `docs/development/phase1/phase1-completion-report.md`
- Bead tree fully updated (all Phase 1 items resolved or carried forward)
- Protocol library ready to be consumed by Phase 2 (Agent) and Phase 3 (Relay)

---

## Anti-Pattern Checklist

| Anti-Pattern | Mitigation |
|---|---|
| Writing implementation before tests | TDD enforced: test file listed before implementation in every step |
| Mutable shared state without synchronization | All shared state protected by sync.Mutex; -race detector in CI |
| Deadlock in flow control | Control frames bypass data windows; context cancellation on all blocking ops |
| Function too large (> 50 lines) | Verified in Step 6; decompose into helpers |
| File too large (> 800 lines) | Split by concern: codec, window, stream, mux, write_queue, udp |
| Hardcoded test values without explanation | Table-driven tests with descriptive names |
| Ignoring edge cases in binary protocol | Tests for truncated reads, max payload, overflow, all commands |
| Tight coupling to TLS | Muxer accepts io.ReadWriteCloser, not *tls.Conn -- testable with net.Pipe |

---

## Plan Mutation Protocol

If requirements change during execution:

1. **Split a step**: Create sub-steps (e.g., 3a, 3b) with clear boundaries
2. **Insert a step**: Add between existing steps, update dependency edges
3. **Skip a step**: Mark as SKIPPED with rationale, verify dependents still work
4. **Reorder**: Only if dependency graph allows (check `Depends on` fields)
5. **Abandon**: Mark as ABANDONED, document reason, clean up any partial work

All mutations must be recorded in this plan file with timestamp and rationale.

---

*Generated: 2026-03-23*
*Blueprint version: 1.0*
*Objective: Implement core wire protocol multiplexing library (Phase 1)*
*Predecessor: plans/atlax-scaffold-construction-plan.md (Phase 0, completed 2026-03-14)*

---

## Execution Log

| Step | Status | Commit | Date |
|------|--------|--------|------|
| Step 1: Frame Codec | COMPLETED | 4822d7e | 2026-03-23 |
| Step 2: Flow Control | COMPLETED | 28cbb80 | 2026-03-23 |
| Step 3: Stream State Machine | COMPLETED | fe0628d | 2026-03-23 |
| Step 4: UDP Framing | COMPLETED | 5d851f4 | 2026-03-23 |
| Step 5: Mux Session | COMPLETED | a6d61e0 | 2026-03-23 |
| Step 6: Integration & Ship | COMPLETED | PR #14 merged | 2026-03-23 |
