# Phase 2 Completion Report: Agent Implementation

**Date:** 2026-03-29
**Status:** COMPLETE
**Branch:** main (all PRs merged)

---

## Phase Summary

Phase 2 delivered the complete atlax tunnel agent binary (`atlax-agent`) that connects to a relay over mTLS, accepts multiplexed streams, and forwards traffic to local services. The phase also completed three Phase 1 protocol gaps required for real traffic to flow.

### Delivery Stats

- **New Go files:** 16 (8 implementation + 8 test)
- **Modified Go files:** 6 (protocol, auth, config, CI)
- **Test functions:** 65 new across all packages
- **Benchmarks:** 1 (ComputeBackoff)
- **Overall coverage:** 91.6% (target: 90%)
- **PRs merged:** 5 (#18, #19, #20, #21, #22)

### Scaffold Interfaces Satisfied

| Interface | Package | Concrete Type | Verified |
|-----------|---------|---------------|----------|
| TLSConfigurator | pkg/auth | Configurator | mtls.go:76 |
| CertificateStore | pkg/auth | FileStore | certs_impl.go:25 |
| Client | pkg/agent | TunnelClient | client_impl.go:52 |
| ServiceForwarder | pkg/agent | Forwarder | forwarder_impl.go:25 |
| Tunnel | pkg/agent | TunnelRunner | tunnel_impl.go:30 |
| Loader | internal/config | FileLoader | loader.go:16 |
| Emitter | internal/audit | SlogEmitter | emitter.go:28 |

All 7 scaffold interfaces satisfied at compile time. Combined with Phase 1 (Muxer, Stream, FrameReader, FrameWriter), all 11 scaffold interfaces now have concrete implementations.

### Files Delivered

| File | Lines | Purpose |
|------|-------|---------|
| pkg/auth/certs_impl.go | 134 | FileStore: cert/CA loading, rotation polling |
| pkg/auth/identity.go | 41 | ExtractIdentity: CN parsing, fingerprint |
| pkg/auth/mtls.go | 139 | Configurator: server/client TLS config builder |
| pkg/agent/backoff.go | 43 | Pure exponential backoff with jitter |
| pkg/agent/client_impl.go | 261 | TunnelClient: connect, reconnect, heartbeat |
| pkg/agent/forwarder_impl.go | 101 | Bidirectional stream-to-TCP copy |
| pkg/agent/tunnel_impl.go | 165 | Stream accept loop + forwarding orchestrator |
| internal/config/loader.go | 108 | YAML config loading + env overrides |
| internal/audit/emitter.go | 108 | Async slog audit emitter |
| cmd/agent/main.go | 182 | Agent binary: startup, shutdown, signal handling |

---

## Consolidated Issue Log

| Step | Issue | Root Cause | Fix |
|------|-------|-----------|-----|
| 1 | CI test failure: certs not found | Dev certs gitignored, CI had no certs/ dir | Added `make certs-dev` to CI workflow |
| 1 | CertificateStore scaffold: single path param | tls.LoadX509KeyPair needs both cert and key paths | Changed interface to (certPath, keyPath) |
| 1 | Wrong client CA handshake test passes | TLS 1.3 server rejection is async; client may not see alert | Assert at least one side fails |
| 2 | agent.AgentClient stutters | Package name + type name repetition | Renamed to TunnelClient |
| 2 | DATA RACE in heartbeat test | remoteConn written in goroutine, read unsynchronized | Removed dead read |
| 3 | Forward blocks after stream.Close() | Close() goes to HalfClosedLocal without cond.Broadcast | Use context cancellation + Reset for teardown |
| 3 | io.CopyBuffer blocks after ctx cancel | io.Copy doesn't take context | Close both ends on ctx cancel to unblock Read |
| 3 | CloseWrite panics on net.Pipe | net.Pipe returns *net.pipe, not *net.TCPConn | Type assertion guard |
| 4 | Send-on-closed-channel panic in Emit | Close closes eventCh while Emit select may pick send case | Added closeCh for two-phase close |
| 5 | exitAfterDefer in main | os.Exit after defer prevents cleanup | Refactored to run() pattern |
| 5 | Config structs missing YAML tags | Scaffold had no yaml tags | Added tags to all fields |

---

## Consolidated Decision Log

1. **StreamSession.writeOut: buffered channel (cap 64)** -- Replaces Phase 1 slice. Enables drain goroutine without polling.
2. **OpenStream: pendingOpen map + ackCh** -- Per-stream channel for ACK wait with context cancellation.
3. **CertificateStore: two-path LoadCertificate** -- Scaffold had single path; Go stdlib needs both.
4. **FileStore: polling for cert rotation** -- Cross-platform; fsnotify unreliable on network FS/Docker.
5. **TunnelClient (not AgentClient)** -- Avoids stutter; interface unchanged.
6. **Dialer interface** -- Abstracts TLS dialing for in-memory testing via net.Pipe.
7. **Heartbeat: no auto-reconnect** -- Separation of concerns; caller handles recovery.
8. **BackoffConfig: pure function** -- No state, deterministic with zero jitter, easy to test.
9. **Forwarder: Reset for forced teardown** -- Close doesn't unblock Read; Reset does.
10. **SlogEmitter: two-phase close with closeCh** -- Prevents send-on-closed-channel panic.
11. **run() pattern in main** -- Ensures deferred cleanup runs; satisfies gocritic.
12. **Single-service routing** -- Phase 2 scope; multi-service deferred to Phase 3.
13. **Env var overrides** -- ATLAX_RELAY_ADDR, ATLAX_TLS_CERT/KEY/CA, ATLAX_LOG_LEVEL.

---

## Open Items Carried Forward

### From Phase 1 (deferred through Phase 2)

| Item | Deferred To | Rationale |
|------|-------------|-----------|
| Stream ID exhaustion / recycling | Phase 5 | ~24 days before overflow at max load |
| sync.Pool for Frame objects | Phase 5 | Optimize after load testing |
| Fuzz testing for FrameCodec | Security phase | Not blocking functionality |

### From Phase 2 (new for Phase 3)

| Item | Severity | Description |
|------|----------|-------------|
| STREAM_CLOSE wire emission | High | Stream.Close() does local transition only; peer never learns stream closed gracefully. MuxSession must enqueue STREAM_CLOSE frame on Close(). ~20 lines. |
| Multi-service routing | High | resolveTarget returns sole service. Relay must send service name in STREAM_OPEN payload; agent parses and routes. |
| Auto-reconnect on heartbeat failure | High | Heartbeat exits goroutine on failure. Supervision loop needed in Tunnel or main to call Reconnect. |
| CustomerID in Status | Medium | Not populated; requires ExtractIdentity on real tls.Conn |

### From Phase 2 (deferred to Phase 5)

| Item | Description |
|------|-------------|
| IdleTimeout for Forwarder | Config field exists, not wired; requires net.Conn deadline wrapping |
| Full cert rotation test | Current test verifies callback on file change only, not CSR/sign cycle |

---

## Coverage Report

| Package | Coverage | Tests |
|---------|----------|-------|
| pkg/protocol | 92.4% | 101 |
| pkg/auth | 94.3% | 25 |
| pkg/agent | 87.0% | 27 |
| internal/config | 100.0% | 15 |
| internal/audit | 96.3% | 8 |
| **Total** | **91.6%** | **176** |

`pkg/agent` at 87%: gap is TLSDialer.DialContext (0%, tested via auth), Tunnel.Start/Stop paths requiring relay, and the ForwarderResetelse branch. All intentional -- these paths are covered by integration tests in CI or require Phase 3 relay.

---

## CI Verification

All CI jobs pass on main after final merge:
- **Lint** (golangci-lint v2.11.3): 0 issues
- **Test** (go test -race -coverprofile): all packages pass
- **Vet + Staticcheck**: clean
- **Security** (govulncheck): clean
- **Build** (linux/amd64, linux/arm64, darwin/arm64): all pass
- **Docker**: relay and agent images build, Trivy scan clean

---

## Lessons Learned

### What Worked Well

1. **Dialer interface for testing** -- Injecting net.Pipe via a Dialer interface eliminated the need for real TLS in unit tests. Tests run in ~5s total, deterministic, no network dependencies.

2. **Parallel Steps 3+4** -- Service Forwarder and Audit Emitter had zero file overlap. Working them in parallel saved a full session.

3. **TDD caught the Emit panic** -- The `TestSlogEmitter_EmitAfterCloseReturnsError` test triggered the send-on-closed-channel race that would have been a production crash. Found in CI, not in local tests (race detector timing-dependent).

### What Was Harder Than Expected

1. **Stream.Close vs Reset semantics** -- Close() only transitions local state and doesn't unblock blocked readers. This gap required using Reset for forced teardown, which is a hard abort. The graceful close path (STREAM_CLOSE on wire) is still missing.

2. **Go select non-determinism** -- The Emit panic was caused by assuming one select case would "win" over another. Go select with multiple ready cases is explicitly non-deterministic. Required a structural fix (closeCh), not a timing fix.

3. **Documentation gap** -- Five steps were completed before any step reports were written. Post-green documentation must happen immediately after each step, not batched.

### What Should Change in Phase 3

1. **Wire STREAM_CLOSE emission first** -- Before any relay logic, complete the stream close handshake. It blocks graceful teardown in every component.

2. **Multi-service routing in STREAM_OPEN** -- The relay must encode the target service name in the STREAM_OPEN payload. Design this before implementing relay listeners.

3. **Reconnection supervision** -- Add a loop in Tunnel.Start that detects heartbeat failure and calls client.Reconnect instead of exiting.

4. **Write step reports immediately** -- Do not batch. Each step's PR should include its report.

---

**Status: READY FOR PHASE 3**
