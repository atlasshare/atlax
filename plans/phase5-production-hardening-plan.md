# Blueprint: Phase 5 -- Production Hardening

**Objective:** Make atlax production-ready. Wire observability into the runtime, add automatic agent reconnection, enforce idle timeouts, prevent stream ID exhaustion, validate certificate rotation, and load test to 1000+ concurrent streams. After this phase, both binaries are deployable to production with confidence.

**Status:** NOT STARTED
**Target duration:** 2-3 weeks
**Estimated sessions:** 5-8

**Prerequisites:** Phase 4 complete (merged to main).

**Related issues:** #31, #32, #34, #35, #36, #41

---

## Scope

### In scope (Phase 5)

| Issue | Item | Step |
|-------|------|------|
| #41 | Health check endpoint (/healthz on admin port) | Step 1 |
| -- | Wire Metrics into router, registry, client_listener | Step 1 |
| -- | Per-customer rate limit config in YAML | Step 1 |
| #36 | Agent reconnection supervision loop | Step 2 |
| #34 | IdleTimeout for Forwarder | Step 3 |
| #31 | Stream ID exhaustion / recycling | Step 4 |
| #35 | Full cert rotation test (self-signed, no external CA) | Step 5 |
| -- | Load testing: 1000+ concurrent streams | Step 6 |

### Deferred (not Phase 5)

| Issue | Item | Why |
|-------|------|-----|
| #32 | sync.Pool for Frame objects | Optimize only if load testing reveals allocation pressure |
| #33 | Fuzz testing for FrameCodec | Separate security phase |
| #40 | Dynamic port allocation (admin API) | Requires full admin API design; Phase 6 |
| -- | Multi-agent support (max_connections > 1) | Requires registry rework; Phase 6 |
| -- | Graceful restarts (zero-downtime binary swap) | Requires fd passing or SO_REUSEPORT; Phase 6 |

---

## Dependency Graph

```
Step 1 (Metrics wiring + health check + rate limit config)
   |
   +--> Step 2 (Agent reconnection supervision)
   |
   +--> Step 3 (IdleTimeout for Forwarder)
   |
   Step 4 (Stream ID recycling)  [independent]
   |
   Step 5 (Cert rotation test)   [independent]
   |
   Step 6 (Load testing + integration verification)  [depends on all]
```

Steps 2, 3, 4, 5 are mostly independent after Step 1. Step 6 depends on all.

---

## Invariants (verified after EVERY step)

1. `go build ./...` passes
2. `go vet ./...` passes
3. `go test -race ./...` passes
4. `golangci-lint run ./...` passes
5. Coverage for changed packages >= 90%
6. No function > 50 lines, no file > 800 lines
7. Step report written immediately

---

## Step 1: Metrics Wiring + Health Check + Rate Limit Config -- TDD

**Branch:** `phase5/metrics-health`
**Depends on:** Phase 4 (main)
**Serial:** yes (metrics wiring touches router, registry, client_listener)

### Context Brief

Three deliverables:

**Wire Metrics into production code:** The `Metrics` struct from Phase 4 exists but is never called. Wire `StreamOpened`/`StreamClosed` into `PortRouter.Route`, `ConnectionRegistered`/`ConnectionUnregistered` into `MemoryRegistry.Register`/`Unregister`, and `ClientRejected` into `ClientListener.handleClient` (rate limit and stream limit rejections).

**Health check endpoint:** Add `/healthz` on the admin port (`admin_addr` in config). Returns 200 with `{"status":"ok","agents":N,"streams":N}`. Used by load balancers and monitoring. Closes #41.

**Per-customer rate limit config:** Move rate limit parameters from hardcoded startup values to `CustomerConfig` in YAML:

```yaml
customers:
  - id: customer-001
    rate_limit:
      requests_per_second: 100
      burst: 50
```

### Tasks

#### Metrics wiring

- [ ] PortRouter gains optional `*Metrics` field
- [ ] Route calls `StreamOpened` on success, `ClientRejected` on stream limit
- [ ] Route cleanup (deferred or on copy completion) calls `StreamClosed`
- [ ] MemoryRegistry gains optional `*Metrics` field
- [ ] Register calls `ConnectionRegistered`, Unregister calls `ConnectionUnregistered`
- [ ] ClientListener calls `ClientRejected("rate_limited")` on rate limit rejection
- [ ] Prometheus HTTP handler on admin port via `promhttp.Handler()`

#### Health check

- [ ] `/healthz` endpoint on admin port
- [ ] Returns JSON: status, agent count, total active streams
- [ ] cmd/relay starts HTTP server on `admin_addr` with `/healthz` and `/metrics`

#### Rate limit config

- [ ] Add `RateLimitConfig` struct: `RequestsPerSecond float64`, `Burst int`
- [ ] Add `RateLimit RateLimitConfig` to `CustomerConfig`
- [ ] ClientListener creates per-customer IPRateLimiter from config (or global fallback)
- [ ] Tests for config loading and per-customer rate limiting

### Exit Criteria

- Metrics increment during real routing (verified by test)
- `/healthz` returns correct agent/stream counts
- `/metrics` serves Prometheus format
- Rate limits configurable per customer in YAML
- Closes #41
- Step report written

---

## Step 2: Agent Reconnection Supervision -- TDD

**Branch:** `phase5/reconnection`
**Depends on:** Step 1 (for metrics on reconnection events)

### Context Brief

When the heartbeat detects a dead connection, the agent currently exits its heartbeat goroutine silently. The process stays alive but idle. With systemd `Restart=always`, recovery takes 5-10 seconds (full process restart). In-process reconnection recovers in 1-2 seconds (backoff + handshake only).

Add a supervision loop that detects heartbeat failure and calls `client.Reconnect`, then restarts the tunnel's accept loop on the new MuxSession. Closes #36.

### Tasks

#### Heartbeat notification

- [ ] TunnelClient gains `OnDisconnect` callback (called by heartbeat goroutine on failure)
- [ ] Alternative: heartbeat writes to a channel that the tunnel reads

#### Tunnel supervision

- [ ] TunnelRunner.Start wraps the accept loop in a reconnection loop
- [ ] On heartbeat failure: teardown current mux, call Reconnect, get new Mux, restart accept
- [ ] Configurable max reconnection attempts before giving up
- [ ] Audit events for reconnection attempts

#### Tests

- [ ] Test: agent detects dead relay and reconnects automatically
- [ ] Test: tunnel resumes accepting streams after reconnection
- [ ] Test: reconnection respects max attempts
- [ ] Test: reconnection respects context cancellation

### Exit Criteria

- Agent reconnects in-process without systemd restart
- Tunnel resumes accepting streams on new MuxSession
- Closes #36
- Step report written

---

## Step 3: IdleTimeout for Forwarder -- TDD

**Branch:** `phase5/idle-timeout`
**Depends on:** Phase 4 (main)

### Context Brief

`ServiceForwarderConfig.IdleTimeout` exists but is not wired. Forwarding sessions with no data transfer remain open indefinitely. This leaks goroutines and file descriptors for idle clients.

Implement by wrapping the local `net.Conn` with deadline updates on each read/write. If no data flows for `IdleTimeout`, the deadline fires and the connection closes. Closes #34.

### Tasks

- [ ] `type idleConn struct` wrapping `net.Conn` with `SetDeadline` on each Read/Write
- [ ] Forwarder.Forward wraps local conn with idleConn when IdleTimeout > 0
- [ ] Test: idle connection closed after timeout
- [ ] Test: active connection stays open (deadline resets on each transfer)
- [ ] Test: IdleTimeout=0 disables the feature (backward compatible)

### Exit Criteria

- Idle forwarding sessions closed after configured timeout
- Active sessions unaffected
- Closes #34
- Step report written

---

## Step 4: Stream ID Recycling -- TDD

**Branch:** `phase5/stream-recycling`
**Depends on:** Phase 4 (main)

### Context Brief

MuxSession.nextStreamID increments monotonically. After 2^31 streams (~1 billion), IDs overflow. At 1000 concurrent streams with 30s lifetime, this takes ~24 days.

Add a free list of closed stream IDs. OpenStream checks the free list before incrementing. When a stream is removed (maybeRemoveStream), its ID is returned to the free list. Closes #31.

### Tasks

- [ ] Add `freeIDs []uint32` to MuxSession
- [ ] maybeRemoveStream appends ID to freeIDs
- [ ] OpenStream pops from freeIDs before incrementing nextStreamID
- [ ] Test: IDs are recycled after stream close
- [ ] Test: recycled IDs are valid (correct parity: odd for relay, even for agent)
- [ ] Test: high-churn scenario (open/close 10000 streams, verify no overflow)
- [ ] Benchmark: compare allocation with and without recycling

### Exit Criteria

- Stream IDs recycled after close
- No overflow under sustained load
- Closes #31
- Step report written

---

## Step 5: Certificate Rotation Test -- TDD

**Branch:** `phase5/cert-rotation`
**Depends on:** Phase 4 (main)

### Context Brief

`FileStore.WatchForRotation` polls for file changes and calls a reload callback. Phase 2 tested this with a file content change (append newline). Phase 5 tests the full rotation lifecycle: generate new key pair, create new self-signed cert, write to disk, verify hot-reload picks it up.

No external CA needed -- the test generates and signs certs locally. Closes #35.

### Tasks

- [ ] Test helper: generate self-signed cert+key pair programmatically (crypto/x509)
- [ ] Test: write initial cert, start WatchForRotation, replace cert with new one, verify reload called with new cert
- [ ] Test: verify new cert has different fingerprint than original
- [ ] Test: verify TLS connection uses new cert after rotation (start TLS server, rotate cert, new client sees new cert)
- [ ] Shorten WatchForRotation poll interval for tests (10ms)

### Exit Criteria

- Full rotation lifecycle tested: generate, write, detect, reload, verify
- TLS connection uses rotated cert
- Closes #35
- Step report written

---

## Step 6: Load Testing + Integration Verification

**Branch:** `main` (all merged)
**Depends on:** Steps 1-5
**Serial:** yes (final gate)

### Context Brief

Stress test the system with 1000+ concurrent streams. Verify metrics, reconnection, idle timeout, and stream ID recycling under load. Write the Phase 5 completion report.

### Tasks

#### Load test script

- [ ] Write `scripts/load-test.sh` (or Go program) that:
  - Starts relay with test config
  - Starts agent with echo service
  - Opens N concurrent client connections to the relay port
  - Each client sends data, verifies echo, then closes
  - Measures: total time, p50/p95/p99 latency, error rate
- [ ] Target: 1000 concurrent streams with < 1% error rate
- [ ] Run with `-race` to verify no races under load

#### Integration checks

- [ ] All tests pass with -race
- [ ] Coverage report
- [ ] Build both binaries
- [ ] Verify all interfaces satisfied
- [ ] Verify metrics increment under load
- [ ] Verify health check shows correct counts under load

#### Decision: sync.Pool (#32)

- [ ] Run load test and check `go tool pprof` allocation profile
- [ ] If Frame allocations dominate: implement sync.Pool and re-benchmark
- [ ] If not: close #32 as "won't fix" with data

### Post-Green Documentation

Phase 5 completion report in `docs/development/phase5/phase5-completion-report.md`:

- [ ] Phase summary, delivery stats
- [ ] Consolidated issue log, decision log
- [ ] Open items carried forward to Phase 6 (#32 if deferred, #33, #40, multi-agent, graceful restarts)
- [ ] Load test results: throughput, latency percentiles, error rate
- [ ] Coverage report
- [ ] Lessons learned

### Exit Criteria

- 1000+ concurrent streams with < 1% error rate
- Metrics, health check, reconnection, idle timeout all verified under load
- Phase 5 completion report written
- Both binaries production-ready

---

## Anti-Pattern Checklist

| Anti-Pattern | Mitigation |
|---|---|
| Metrics in hot path causing latency | Counter/gauge increments are atomic; no lock contention |
| Health check blocks on slow registry | Health check reads NumStreams (lock-free atomic) and agent count (RLock) |
| Reconnection infinite loop | Max attempts config; backoff with jitter; context cancellation |
| IdleTimeout too aggressive | Default disabled (0); must be explicitly configured |
| Stream ID free list grows unbounded | Free list is bounded by max concurrent streams (closed IDs recycled, not accumulated) |
| Load test flaky in CI | Load test is a manual script, not part of `go test`; results documented in report |

---

## Plan Mutation Protocol

Same as previous phases. All mutations recorded with timestamp and rationale.

---

## Execution Log

| Step | Status | PR | Date |
|------|--------|----|------|
| Step 1: Metrics + health + rate config | NOT STARTED | -- | -- |
| Step 2: Reconnection supervision | NOT STARTED | -- | -- |
| Step 3: IdleTimeout | NOT STARTED | -- | -- |
| Step 4: Stream ID recycling | NOT STARTED | -- | -- |
| Step 5: Cert rotation test | NOT STARTED | -- | -- |
| Step 6: Load testing + ship | NOT STARTED | -- | -- |

---

*Generated: 2026-04-02*
*Blueprint version: 1.0*
*Objective: Production-ready relay and agent with observability, resilience, and load-tested performance (Phase 5)*
*Predecessor: plans/phase4-multi-tenancy-plan.md (Phase 4, completed 2026-04-01)*
