# Blueprint: Phase 2 -- Agent Implementation

**Objective:** Implement the atlax tunnel agent (`atlax-agent` binary) that connects to a relay over mTLS, receives stream requests, and forwards traffic to local services. This phase also completes the Phase 1 protocol gaps (STREAM_OPEN handshake, write draining, flow window integration) that are required for real traffic to flow.

**Status:** NOT STARTED
**Target duration:** 3 weeks
**Estimated sessions:** 5-8 (steps 1-2 serial, steps 3-4 parallelizable, steps 5-6 serial)

**Prerequisites:** Phase 1 complete (merged to main). All prerequisite tasks in `plans/prereqs/phase2/` must be completed by the user before execution begins.

---

## Dependency Graph

```
Step 1 (Protocol Completions + mTLS Auth)
   |
   v
Step 2 (Agent Client -- Connect, Reconnect, Heartbeat)
   |
   +--> Step 3 (Service Forwarder)  --------+
   |                                         |
   +--> Step 4 (Audit Emitter)              +--> Step 6 (Integration & Ship)
   |                                         |
   Step 5 (Config Loader + cmd/agent)  -----+
```

**Parallelism:** Steps 3 and 4 share no files and can execute concurrently. Step 5 depends on Steps 2 and 3 (it wires them together). Step 6 is the final gate.

---

## Naming Conventions (enforced across ALL steps)

- **New file naming:** Implementation files use descriptive suffixes. Test files use `_test` suffix.
- **Type naming:** Concrete types are named directly (e.g., `TLSConfigurator`, `AgentClient`, `Forwarder`). No "Impl" suffix.
- **Error wrapping:** All errors use `fmt.Errorf("component: operation: %w", err)` pattern.
- **Logging:** All production code uses `log/slog` structured logging. No `fmt.Println`.
- **Context:** All functions performing I/O accept `context.Context` as first parameter.

---

## Invariants (verified after EVERY step)

1. `go build ./...` passes
2. `go vet ./...` passes
3. `go test -race ./...` passes (ALL packages, not just the one being worked on)
4. `gofmt -l .` returns no output
5. `golangci-lint run ./...` passes
6. Coverage for changed packages >= 90%
7. No function exceeds 50 lines
8. No file exceeds 800 lines
9. No hardcoded secrets or private keys
10. No emoji in code, comments, or test output
11. All new types satisfy existing scaffold interfaces where applicable
12. All I/O functions propagate context.Context

---

## Step 1: Protocol Completions + mTLS Authentication -- TDD

**Branch:** `phase2/auth-protocol`
**Depends on:** Phase 1 (main)
**Model tier:** strongest (mTLS correctness is security-critical, protocol gap fixes affect all downstream steps)
**Serial:** yes (all agent steps depend on working auth and a complete mux protocol)
**Rollback:** `git branch -D phase2/auth-protocol`

### Context Brief

This step has two halves:

**Half A -- Protocol gap fixes in `pkg/protocol/`:**
Complete the three Phase 1 carry-forward items that block real traffic:
1. STREAM_OPEN handshake: OpenStream must send STREAM_OPEN and block until peer sends STREAM_OPEN+ACK. AcceptStream must send STREAM_OPEN+ACK back.
2. Write draining: MuxSession needs a mechanism to read each stream's write buffer and emit STREAM_DATA frames through the write queue.
3. Flow window integration: handleWindowUpdate must call window.Update() on the correct stream or connection.

**Half B -- mTLS in `pkg/auth/`:**
Implement the TLSConfigurator and CertificateStore interfaces defined in the scaffold. The TLSConfigurator produces `*tls.Config` for both relay-side (server) and agent-side (client) mTLS. The CertificateStore loads PEM files and supports hot-reload via file watching. ExtractIdentity reads CN=customer-{uuid} from peer certs.

**Security docs reference:** `docs/security/mtls.md`, `docs/security/cert-lifecycle.md`
**Protocol reference:** `docs/development/phase1/phase1-completion-report.md` section "Open Items Carried Forward"

**Critical rules:**
- TLS 1.3 minimum, no fallback
- ClientAuth = RequireAndVerifyClientCert on relay side
- Agent side verifies relay cert against Relay Intermediate CA (not Root CA directly)
- ExtractIdentity must validate CN format: `customer-{uuid}`
- Certificate loading must not panic on missing files (return error)
- WatchForRotation must use polling (not fsnotify) for cross-platform reliability

### Tasks

#### Half A: Protocol Completions

##### Test updates: `pkg/protocol/mux_session_test.go`

Add tests for the three gap fixes:

- [ ] Test OpenStream blocks until peer sends STREAM_OPEN+ACK
- [ ] Test OpenStream times out via context if no ACK received
- [ ] Test AcceptStream sends STREAM_OPEN+ACK back to opener
- [ ] Test bidirectional data transfer through pipe-connected muxers (write on one side, read on other)
- [ ] Test WINDOW_UPDATE frame increments the correct stream's send window
- [ ] Test WINDOW_UPDATE on stream ID 0 increments connection-level window
- [ ] Test WINDOW_UPDATE with invalid stream ID is ignored (no panic)
- [ ] Test write draining: data written to stream appears as STREAM_DATA frames on transport
- [ ] Test large write is split into multiple STREAM_DATA frames respecting MaxFrameSize
- [ ] Test flow control: write blocks when send window exhausted, unblocks on WINDOW_UPDATE

##### Implementation updates: `pkg/protocol/mux_session.go`, `pkg/protocol/stream_impl.go`

- [ ] Add pending open tracking: map of stream ID -> chan for ACK notification
- [ ] Update OpenStream to send STREAM_OPEN frame and wait for ACK (with ctx timeout)
- [ ] Update handleFrame: on STREAM_OPEN from peer, create stream, enqueue to acceptCh, send STREAM_OPEN+ACK
- [ ] Update handleFrame: on STREAM_OPEN+ACK, notify pending open channel
- [ ] Add drain goroutine: reads from each stream's write output channel, builds STREAM_DATA frames, enqueues to write queue
- [ ] Add StreamSession.WriteCh() or similar exported method for MuxSession to consume written data
- [ ] Implement handleWindowUpdate: lookup stream by ID, call sendWindow.Update(increment)
- [ ] Handle connection-level WINDOW_UPDATE (stream ID 0)

#### Half B: mTLS Authentication

##### Test file: `pkg/auth/mtls_test.go`

Write tests FIRST:

- [ ] Test ServerTLSConfig returns config with MinVersion=TLS13 and ClientAuth=RequireAndVerifyClientCert
- [ ] Test ServerTLSConfig includes relay cert in Certificates
- [ ] Test ServerTLSConfig includes customer CA in ClientCAs pool
- [ ] Test ClientTLSConfig returns config with MinVersion=TLS13
- [ ] Test ClientTLSConfig includes customer cert in Certificates
- [ ] Test ClientTLSConfig includes relay CA in RootCAs pool
- [ ] Test ClientTLSConfig sets ServerName from config
- [ ] Test ClientTLSConfig with WithSessionCache option enables session caching
- [ ] Test ServerTLSConfig with invalid cert path returns error
- [ ] Test ClientTLSConfig with invalid cert path returns error
- [ ] Test full mTLS handshake: start TLS server with ServerTLSConfig, connect with ClientTLSConfig, verify handshake succeeds
- [ ] Test mTLS handshake fails when client cert is signed by wrong CA
- [ ] Test mTLS handshake fails when server cert is signed by wrong CA

##### Test file: `pkg/auth/identity_test.go`

- [ ] Test ExtractIdentity returns correct CustomerID from CN=customer-{uuid}
- [ ] Test ExtractIdentity returns correct RelayID from CN=relay.atlax.local
- [ ] Test ExtractIdentity returns error when no peer certificates
- [ ] Test ExtractIdentity returns error when CN format is invalid
- [ ] Test ExtractIdentity populates CertFingerprint (SHA-256)
- [ ] Test ExtractIdentity populates NotBefore and NotAfter from cert

##### Test file: `pkg/auth/certs_test.go`

- [ ] Test LoadCertificate with valid PEM files returns tls.Certificate
- [ ] Test LoadCertificate with missing cert file returns error
- [ ] Test LoadCertificate with missing key file returns error
- [ ] Test LoadCertificate with mismatched cert/key returns error
- [ ] Test LoadCertificateAuthority with valid CA PEM returns x509.CertPool
- [ ] Test LoadCertificateAuthority with missing file returns error
- [ ] Test LoadCertificateAuthority with invalid PEM returns error
- [ ] Test WatchForRotation calls reload on file change (mock with temp files)
- [ ] Test WatchForRotation respects context cancellation

##### Implementation file: `pkg/auth/mtls.go`

- [ ] `type TLSConfig struct` -- holds cert paths and options
- [ ] `type Configurator struct` -- satisfies TLSConfigurator interface
- [ ] `func NewConfigurator(store CertificateStore, cfg TLSConfig) *Configurator`
- [ ] `func (c *Configurator) ServerTLSConfig(opts ...TLSOption) (*tls.Config, error)` -- load relay cert, customer CA pool, set MinVersion=TLS13, ClientAuth=RequireAndVerifyClientCert
- [ ] `func (c *Configurator) ClientTLSConfig(opts ...TLSOption) (*tls.Config, error)` -- load customer cert, relay CA pool, set ServerName, MinVersion=TLS13
- [ ] `func WithSessionCache(size int) TLSOption` -- enables LRU session cache
- [ ] `func WithMinVersion(version uint16) TLSOption` -- override min version (testing only)
- [ ] Compile-time interface check: `var _ TLSConfigurator = (*Configurator)(nil)`

##### Implementation file: `pkg/auth/identity.go`

- [ ] `func ExtractIdentity(conn *tls.Conn) (*Identity, error)` -- replace stub
- [ ] Extract CN from PeerCertificates[0].Subject.CommonName
- [ ] Parse customer ID: validate `customer-{uuid}` format
- [ ] Parse relay ID: validate `relay.` prefix
- [ ] Compute SHA-256 fingerprint of raw cert bytes
- [ ] Populate NotBefore, NotAfter from cert

##### Implementation file: `pkg/auth/certs.go`

- [ ] `type FileStore struct` -- satisfies CertificateStore interface
- [ ] `func NewFileStore() *FileStore`
- [ ] `func (s *FileStore) LoadCertificate(path string) (tls.Certificate, error)` -- tls.LoadX509KeyPair with error wrapping
- [ ] `func (s *FileStore) LoadCertificateAuthority(path string) (*x509.CertPool, error)` -- read PEM, parse, add to pool
- [ ] `func (s *FileStore) WatchForRotation(ctx context.Context, certPath, keyPath string, reload func(tls.Certificate)) error` -- polling loop with configurable interval
- [ ] Compile-time interface check: `var _ CertificateStore = (*FileStore)(nil)`

### Verification

```bash
go test -race -v -count=3 ./pkg/auth/...
go test -race -v -count=3 ./pkg/protocol/... -run "TestOpenStream|TestWriteDrain|TestWindowUpdate|TestBidirectional"
go test -race -coverprofile=coverage.out ./pkg/auth/... ./pkg/protocol/...
go tool cover -func=coverage.out | grep -E "mtls|identity|certs|mux_session|stream_impl"
go vet ./...
golangci-lint run ./...
```

### Post-Green Documentation

After all tests pass and CI is green, write a step report in `docs/development/phase2/step1-auth-protocol-report.md`:

- [ ] **Summary:** What was implemented (mTLS auth + protocol gap fixes)
- [ ] **Issues encountered:** Every bug, TLS configuration issue, race condition, and test failure. Include symptom, root cause, and fix.
- [ ] **Decisions made:** TLS option API design, polling vs fsnotify for cert watch, write drain goroutine architecture, STREAM_OPEN handshake timeout defaults.
- [ ] **Deviations from plan:** Any changes to the scaffold interfaces or added types.
- [ ] **Coverage report:** Paste `go tool cover -func` output for auth/ and protocol/ changed files.

### Exit Criteria

- Full mTLS handshake works with dev certs (integration test)
- ExtractIdentity correctly parses customer and relay identities
- Certificate hot-reload works via file polling
- STREAM_OPEN handshake completes (bidirectional ACK)
- Data written to a stream appears as STREAM_DATA on the transport
- WINDOW_UPDATE frames update the correct flow window
- Coverage >= 90% for all changed files
- All invariants pass
- Step report written
- PR: `phase2/auth-protocol` -> `main`

---

## Step 2: Agent Client -- Connection, Reconnection, Heartbeat -- TDD

**Branch:** `phase2/agent-client`
**Depends on:** Step 1
**Model tier:** strongest (reconnection logic with backoff is subtle, heartbeat timing is correctness-critical)
**Serial:** yes (forwarder and tunnel depend on a working client)
**Rollback:** `git branch -D phase2/agent-client`

### Context Brief

Implement the concrete Client that manages the agent's persistent connection to the relay. The client handles initial connection (mTLS handshake + MuxSession creation), automatic reconnection with exponential backoff and jitter, and heartbeat (PING/PONG) monitoring. The scaffold `Client` interface in `pkg/agent/client.go` defines the contract.

The client does NOT manage streams or forward traffic -- that is the Tunnel's job (Step 3). The client owns the transport lifecycle: connect, disconnect, reconnect, health monitoring.

**Reference:** `docs/security/mtls.md` for TLS config, `configs/agent.example.yaml` for reconnect/keepalive defaults.

**Critical rules:**
- Reconnection uses exponential backoff: base * 2^attempt, capped at max, with jitter
- Jitter is random in [0, backoff/2] added to computed backoff (prevents thundering herd)
- Heartbeat runs as a background goroutine during active connection
- If heartbeat fails (PONG not received within timeout), trigger reconnect
- All blocking operations respect context cancellation
- Connection state transitions must be logged with slog

### Tasks

#### Test file: `pkg/agent/client_test.go`

Write tests FIRST:

- [ ] Test NewClient creates client in disconnected state
- [ ] Test Connect establishes MuxSession over mTLS (use in-memory TLS pipe)
- [ ] Test Connect fails if TLS handshake fails (wrong CA)
- [ ] Test Connect sets status to Connected with correct fields
- [ ] Test Reconnect tears down existing connection and establishes new one
- [ ] Test Reconnect uses exponential backoff (verify delays with mock clock or short intervals)
- [ ] Test Reconnect adds jitter to backoff (verify non-deterministic delay)
- [ ] Test Reconnect caps backoff at MaxReconnectBackoff
- [ ] Test Reconnect respects MaxReconnectAttempts (returns error after exhaustion)
- [ ] Test Reconnect respects context cancellation while backing off
- [ ] Test Close gracefully shuts down MuxSession and stops heartbeat
- [ ] Test Close is idempotent
- [ ] Test Status returns correct snapshot at each lifecycle point
- [ ] Test heartbeat sends PING at configured interval
- [ ] Test heartbeat triggers reconnect when PONG not received within timeout
- [ ] Test heartbeat stops when connection is closed
- [ ] Test concurrent Connect/Close does not race (run with -race, count=5)

#### Implementation file: `pkg/agent/client_impl.go`

Write implementation SECOND:

- [ ] `type AgentClient struct` -- tlsConfig, mux, config, status, heartbeatCancel, mu, logger
- [ ] `func NewClient(cfg ClientConfig, tlsCfg *tls.Config, logger *slog.Logger) *AgentClient`
- [ ] `func (c *AgentClient) Connect(ctx context.Context) error` -- dial TLS, create MuxSession(RoleAgent), start heartbeat goroutine
- [ ] `func (c *AgentClient) Reconnect(ctx context.Context) error` -- close existing, backoff loop with jitter, call Connect
- [ ] `func (c *AgentClient) Close() error` -- stop heartbeat, send GoAway, close MuxSession
- [ ] `func (c *AgentClient) Status() ClientStatus` -- return snapshot under lock
- [ ] `func (c *AgentClient) Mux() protocol.Muxer` -- returns current MuxSession (used by Tunnel)
- [ ] Internal: `func (c *AgentClient) runHeartbeat(ctx context.Context)` -- goroutine: PING at interval, trigger reconnect on timeout
- [ ] Internal: `func (c *AgentClient) computeBackoff(attempt int) time.Duration` -- exponential with jitter
- [ ] Compile-time interface check: `var _ Client = (*AgentClient)(nil)`

#### Helper file: `pkg/agent/backoff.go`

- [ ] `type BackoffConfig struct` -- InitialInterval, MaxInterval, Multiplier, JitterFraction
- [ ] `func ComputeBackoff(cfg BackoffConfig, attempt int) time.Duration` -- pure function, deterministic base + random jitter
- [ ] `func DefaultBackoffConfig() BackoffConfig` -- 5s initial, 300s max, 2x multiplier, 0.5 jitter

#### Test file: `pkg/agent/backoff_test.go`

- [ ] Test backoff at attempt 0 returns InitialInterval (plus jitter)
- [ ] Test backoff grows exponentially (attempt 1 = 2x, attempt 2 = 4x, etc.)
- [ ] Test backoff caps at MaxInterval
- [ ] Test jitter is within [0, base*JitterFraction]
- [ ] Test backoff with zero JitterFraction returns exact base
- [ ] Benchmark: BenchmarkComputeBackoff

### Verification

```bash
go test -race -v -count=5 ./pkg/agent/...
go test -race -coverprofile=coverage.out ./pkg/agent/...
go tool cover -func=coverage.out | grep -E "client_impl|backoff"
go vet ./...
golangci-lint run ./...
```

### Post-Green Documentation

Write step report in `docs/development/phase2/step2-agent-client-report.md`:

- [ ] **Summary:** Client lifecycle, backoff algorithm, heartbeat design
- [ ] **Issues encountered:** TLS pipe testing challenges, timing-sensitive test issues, race conditions
- [ ] **Decisions made:** Backoff algorithm choice, heartbeat goroutine lifecycle, Mux() accessor design
- [ ] **Deviations from plan**
- [ ] **Coverage report**

### Exit Criteria

- AgentClient connects to relay over mTLS (integration test with in-memory pipe)
- Reconnection with exponential backoff + jitter works
- Heartbeat detects dead connections and triggers reconnect
- Close is graceful (GoAway sent, streams drained)
- Coverage >= 90% for client_impl.go and backoff.go
- All invariants pass
- PR: `phase2/agent-client` -> `main`

---

## Step 3: Service Forwarder -- Bidirectional Copy -- TDD

**Branch:** `phase2/service-forwarder`
**Depends on:** Step 1 (protocol completions for working Stream read/write)
**Model tier:** default (straightforward bidirectional copy pattern)
**Parallel with:** Step 4
**Rollback:** `git branch -D phase2/service-forwarder`

### Context Brief

Implement the ServiceForwarder that copies data bidirectionally between a multiplexed Stream and a local TCP service (e.g., Samba on 127.0.0.1:445). The forwarder is the innermost loop of the tunnel: for each incoming stream, dial the local service and copy bytes in both directions until one side closes.

The scaffold `ServiceForwarder` interface in `pkg/agent/forwarder.go` defines the contract.

**Critical rules:**
- Forward must handle half-close correctly: if local service closes write side, close stream write side (and vice versa)
- Use `io.Copy` with a configurable buffer size (default 32KB)
- DialTimeout prevents hanging on unresponsive local services
- IdleTimeout closes stale forwarding sessions
- Bytes transferred must be tracked for stats
- Both copy directions run concurrently; when either finishes, the other must be terminated

### Tasks

#### Test file: `pkg/agent/forwarder_test.go`

Write tests FIRST:

- [ ] Test Forward copies data from stream to local service
- [ ] Test Forward copies data from local service to stream
- [ ] Test Forward copies data bidirectionally
- [ ] Test Forward closes local conn when stream closes
- [ ] Test Forward closes stream when local conn closes
- [ ] Test Forward respects DialTimeout (local service not listening)
- [ ] Test Forward respects context cancellation
- [ ] Test Forward with zero-length reads (EOF)
- [ ] Test Forward with large payload (> buffer size, requires multiple reads)
- [ ] Test concurrent Forward calls do not race

#### Implementation file: `pkg/agent/forwarder_impl.go`

Write implementation SECOND:

- [ ] `type Forwarder struct` -- config, logger, dialer
- [ ] `func NewForwarder(cfg ServiceForwarderConfig, logger *slog.Logger) *Forwarder`
- [ ] `func (f *Forwarder) Forward(ctx context.Context, stream protocol.Stream, target string) error`
  - Dial target with timeout
  - Launch two goroutines: stream->local, local->stream
  - Wait for either to finish, cancel the other
  - Close both connections
  - Return first error (or nil if clean close)
- [ ] Internal: `func (f *Forwarder) copy(ctx context.Context, dst io.Writer, src io.Reader, buf []byte) (int64, error)` -- context-aware copy
- [ ] Compile-time interface check: `var _ ServiceForwarder = (*Forwarder)(nil)`

### Verification

```bash
go test -race -v -count=3 ./pkg/agent/... -run TestForward
go test -race -coverprofile=coverage.out ./pkg/agent/...
go tool cover -func=coverage.out | grep forwarder_impl
go vet ./...
golangci-lint run ./...
```

### Post-Green Documentation

Write step report in `docs/development/phase2/step3-service-forwarder-report.md`:

- [ ] **Summary:** Bidirectional copy architecture, half-close handling
- [ ] **Issues encountered:** Copy goroutine lifecycle, context propagation, error handling on dual-close
- [ ] **Decisions made:** Buffer size, copy strategy (io.Copy vs manual loop), error priority (which goroutine's error is returned)
- [ ] **Coverage report**

### Exit Criteria

- Bidirectional copy works end-to-end (stream <-> local TCP)
- Half-close propagates correctly
- DialTimeout prevents hanging
- Context cancellation stops forwarding
- Coverage >= 90% for forwarder_impl.go
- All invariants pass
- PR: `phase2/service-forwarder` -> `main`

---

## Step 4: Audit Emitter -- Community Implementation -- TDD

**Branch:** `phase2/audit-emitter`
**Depends on:** Phase 1 (scaffold defines Emitter interface and Event type)
**Model tier:** default (structured logging is straightforward)
**Parallel with:** Step 3
**Rollback:** `git branch -D phase2/audit-emitter`

### Context Brief

Implement the community edition audit Emitter that writes audit events as structured JSON log lines via `log/slog`. The scaffold `Emitter` interface and `Event` type in `internal/audit/audit.go` define the contract. Enterprise editions will inject alternative implementations (event bus, SIEM).

The audit emitter is used by the agent client (Step 2) and tunnel (Step 5) to log connection lifecycle events: connect, disconnect, stream open/close, auth success/failure, cert rotation, heartbeat timeout.

**Critical rules:**
- Events are immutable (Event struct has no pointer fields that could be mutated after emit)
- Emit must not block the caller (async write with bounded buffer)
- Close must flush all pending events before returning
- Each event includes timestamp, action, actor, customerID, and optional metadata
- JSON format only (no text format for audit events)

### Tasks

#### Test file: `internal/audit/audit_test.go`

Write tests FIRST:

- [ ] Test Emit writes structured JSON to output
- [ ] Test Emit includes all Event fields in JSON output
- [ ] Test Emit does not block when buffer is full (drops or applies backpressure -- decide)
- [ ] Test Close flushes all pending events
- [ ] Test Close is idempotent
- [ ] Test Emit after Close returns error
- [ ] Test concurrent Emit calls do not race
- [ ] Test each audit Action constant has correct string value

#### Implementation file: `internal/audit/emitter.go`

Write implementation SECOND:

- [ ] `type SlogEmitter struct` -- logger, eventCh, done, closed
- [ ] `func NewSlogEmitter(logger *slog.Logger, bufferSize int) *SlogEmitter`
- [ ] `func (e *SlogEmitter) Emit(ctx context.Context, event Event) error` -- send to buffered channel
- [ ] `func (e *SlogEmitter) Close() error` -- close channel, wait for drain goroutine
- [ ] Internal: `func (e *SlogEmitter) drainLoop()` -- goroutine: read from channel, write slog.Info with structured attrs
- [ ] Compile-time interface check: `var _ Emitter = (*SlogEmitter)(nil)`

### Verification

```bash
go test -race -v -count=3 ./internal/audit/...
go test -race -coverprofile=coverage.out ./internal/audit/...
go tool cover -func=coverage.out | grep emitter
go vet ./...
golangci-lint run ./...
```

### Post-Green Documentation

Write step report in `docs/development/phase2/step4-audit-emitter-report.md`:

- [ ] **Summary:** Async audit emitter design, buffer strategy
- [ ] **Issues encountered**
- [ ] **Decisions made:** Buffer size default, backpressure vs drop policy, slog attr mapping
- [ ] **Coverage report**

### Exit Criteria

- Audit events written as structured JSON via slog
- Async emit does not block caller
- Close flushes pending events
- Coverage >= 90% for emitter.go
- All invariants pass
- PR: `phase2/audit-emitter` -> `main`

---

## Step 5: Config Loader + Tunnel + cmd/agent -- TDD

**Branch:** `phase2/agent-binary`
**Depends on:** Steps 2, 3, 4
**Model tier:** default (wiring layer, less algorithmic complexity)
**Serial:** yes (integrates all prior steps)
**Rollback:** `git branch -D phase2/agent-binary`

### Context Brief

Three components wired together:

1. **Config Loader** (`internal/config/`): Implements the Loader interface. Reads agent YAML config, validates required fields, applies environment variable overrides (ATLAX_RELAY_ADDR, ATLAX_TLS_CERT, etc.).

2. **Tunnel** (`pkg/agent/`): Implements the Tunnel interface. Orchestrates the Client and Forwarder: accepts streams from the MuxSession, maps each stream to a local service based on the STREAM_OPEN payload (target address), launches a Forwarder per stream, tracks active streams, and handles graceful shutdown.

3. **cmd/agent/main.go**: Wires config loader, TLS configurator, client, tunnel, and audit emitter. Handles signal-based shutdown (SIGINT, SIGTERM).

**Config reference:** `configs/agent.example.yaml`

### Tasks

#### Config Loader

##### Test file: `internal/config/config_test.go`

- [ ] Test LoadAgentConfig with valid YAML returns populated AgentConfig
- [ ] Test LoadAgentConfig with missing file returns error
- [ ] Test LoadAgentConfig with invalid YAML returns error
- [ ] Test LoadAgentConfig validates required fields (relay.addr, tls.cert_file, tls.key_file, tls.ca_file)
- [ ] Test LoadAgentConfig with env var override (ATLAX_RELAY_ADDR overrides relay.addr)
- [ ] Test LoadAgentConfig with empty services list is valid (agent with no services)
- [ ] Test LoadRelayConfig with valid YAML returns populated RelayConfig (stub for Phase 3, basic test only)

##### Implementation file: `internal/config/loader.go`

- [ ] `type FileLoader struct`
- [ ] `func NewFileLoader() *FileLoader`
- [ ] `func (l *FileLoader) LoadAgentConfig(path string) (*AgentConfig, error)` -- read YAML, unmarshal, validate, apply env overrides
- [ ] `func (l *FileLoader) LoadRelayConfig(path string) (*RelayConfig, error)` -- read YAML, unmarshal, validate (minimal, Phase 3 will expand)
- [ ] Internal: `func (l *FileLoader) applyEnvOverrides(cfg *AgentConfig)` -- ATLAX_RELAY_ADDR, ATLAX_TLS_CERT, ATLAX_TLS_KEY, ATLAX_TLS_CA, ATLAX_LOG_LEVEL
- [ ] Internal: `func (l *FileLoader) validateAgentConfig(cfg *AgentConfig) error` -- check required fields non-empty
- [ ] Compile-time interface check: `var _ Loader = (*FileLoader)(nil)`

#### Tunnel

##### Test file: `pkg/agent/tunnel_test.go`

- [ ] Test NewTunnel creates tunnel in stopped state
- [ ] Test Start accepts streams from MuxSession and launches forwarders
- [ ] Test Start maps STREAM_OPEN payload to correct local service address
- [ ] Test Start rejects streams for unmapped services (sends STREAM_RESET)
- [ ] Test Stop closes all active forwarders gracefully
- [ ] Test Stop respects context deadline (force-close after timeout)
- [ ] Test Stats returns correct ActiveStreams, TotalStreams, BytesIn, BytesOut
- [ ] Test concurrent stream acceptance does not race

##### Implementation file: `pkg/agent/tunnel_impl.go`

- [ ] `type AgentTunnel struct` -- client, forwarderCfg, services map, activeStreams, stats, mu, logger
- [ ] `func NewTunnel(client Client, fwdCfg ServiceForwarderConfig, services []ServiceMapping, logger *slog.Logger) *AgentTunnel`
- [ ] `func (t *AgentTunnel) Start(ctx context.Context) error` -- accept loop: AcceptStream, resolve target, launch goroutine with Forwarder.Forward
- [ ] `func (t *AgentTunnel) Stop(ctx context.Context) error` -- cancel accept loop, close all active streams, wait for forwarders
- [ ] `func (t *AgentTunnel) Stats() TunnelStats`
- [ ] Compile-time interface check: `var _ Tunnel = (*AgentTunnel)(nil)`

#### cmd/agent/main.go

##### Implementation: `cmd/agent/main.go`

- [ ] Parse `-config` flag (default: `agent.yaml`)
- [ ] Load config via FileLoader
- [ ] Initialize slog logger from config
- [ ] Create FileStore and Configurator
- [ ] Build ClientTLSConfig
- [ ] Create AgentClient
- [ ] Create Forwarder
- [ ] Create AgentTunnel with service mappings from config
- [ ] Connect to relay
- [ ] Start tunnel
- [ ] Register signal handler (SIGINT, SIGTERM)
- [ ] On signal: Stop tunnel, Close client, flush audit emitter
- [ ] Exit with code 0 on clean shutdown, 1 on error

No unit tests for main.go (integration tested in Step 6).

### Verification

```bash
go test -race -v ./internal/config/... ./pkg/agent/...
go test -race -coverprofile=coverage.out ./internal/config/... ./pkg/agent/...
go tool cover -func=coverage.out | grep -E "loader|tunnel_impl"
go build ./cmd/agent/...
go vet ./...
golangci-lint run ./...
```

### Post-Green Documentation

Write step report in `docs/development/phase2/step5-config-tunnel-cmd-report.md`:

- [ ] **Summary:** Config loading, tunnel orchestration, binary wiring
- [ ] **Issues encountered**
- [ ] **Decisions made:** Env var naming convention, service mapping lookup strategy, shutdown sequence ordering
- [ ] **Coverage report**

### Exit Criteria

- `go build ./cmd/agent/` produces working binary
- Config loads from YAML with env var overrides
- Tunnel accepts streams and forwards to local services
- Graceful shutdown on SIGINT/SIGTERM
- Coverage >= 90% for loader.go and tunnel_impl.go
- All invariants pass
- PR: `phase2/agent-binary` -> `main`

---

## Step 6: Integration Verification and Ship

**Branch:** `main` (all branches merged)
**Depends on:** Steps 1-5
**Model tier:** default
**Serial:** yes (final gate)
**Rollback:** N/A (verification only)

### Context Brief

Final verification after all Phase 2 PRs are merged. Run full test suite, verify coverage, run an end-to-end integration test with the actual agent binary, and confirm readiness for Phase 3 (Relay implementation).

### Merge Strategy

Merge branches in dependency order:
1. **Step 1** (`phase2/auth-protocol`) -- Foundation: mTLS + protocol completions
2. **Step 2** (`phase2/agent-client`) -- Depends on Step 1
3. **Step 3** (`phase2/service-forwarder`) -- Depends on Step 1
4. **Step 4** (`phase2/audit-emitter`) -- Independent
5. **Step 5** (`phase2/agent-binary`) -- Depends on Steps 2, 3, 4

### Tasks

- [ ] Merge all five PRs in order specified above
- [ ] Run full test suite: `go test -race -coverprofile=coverage.out ./...`
- [ ] Verify overall coverage >= 90% for all Phase 2 packages (pkg/auth, pkg/agent, internal/config, internal/audit)
- [ ] Run `go vet ./...` -- no warnings
- [ ] Run `golangci-lint run ./...` -- no errors
- [ ] Run `gofmt -l .` -- no unformatted files
- [ ] Build both binaries: `make build`
- [ ] Verify all scaffold interfaces satisfied:
  - `TLSConfigurator` satisfied by `Configurator`
  - `CertificateStore` satisfied by `FileStore`
  - `Client` satisfied by `AgentClient`
  - `ServiceForwarder` satisfied by `Forwarder`
  - `Tunnel` satisfied by `AgentTunnel`
  - `Loader` satisfied by `FileLoader`
  - `Emitter` satisfied by `SlogEmitter`
- [ ] End-to-end smoke test:
  - Start a mock relay (TLS server that accepts mTLS, creates MuxSession, opens a stream)
  - Start agent binary with config pointing to mock relay and a local echo server
  - Verify data flows through: mock relay -> agent -> echo server -> agent -> mock relay
  - Verify graceful shutdown (send SIGTERM, confirm clean exit)
- [ ] Verify no hardcoded secrets or private keys
- [ ] Verify no emoji in any file
- [ ] Verify no function exceeds 50 lines, no file exceeds 800 lines

### Post-Green Documentation

Write Phase 2 completion report in `docs/development/phase2/phase2-completion-report.md`:

- [ ] **Phase summary:** What Phase 2 delivered, total files added, total test count, overall coverage
- [ ] **Consolidated issue log:** Aggregate all issues from step reports
- [ ] **Consolidated decision log:** Aggregate all decisions from step reports
- [ ] **Open items carried forward:** Must include ALL of the following:
  - **From Phase 1 (deferred through Phase 2):**
    - Stream ID exhaustion / recycling (deferred to Phase 5)
    - sync.Pool for Frame objects (deferred to Phase 5)
    - Fuzz testing for FrameCodec (deferred to security phase)
  - **From Phase 2 (new for Phase 3):**
    - Relay-side mTLS verification (relay uses same auth but as server)
    - Relay-side stream routing (TrafficRouter implementation)
    - Multi-tenant isolation (AgentRegistry + per-customer stream scoping)
    - Any new items discovered during Phase 2 implementation
- [ ] **Architecture snapshot:** Current state of pkg/auth/, pkg/agent/, internal/config/, internal/audit/ -- file list, type list, interface satisfaction map
- [ ] **Performance baseline:** Benchmark results for new code
- [ ] **CI verification:** All CI jobs green on main
- [ ] **Lessons learned:** What went well, what was harder than expected, what should change in Phase 3

### Verification

```bash
cd /Users/rubenyomenou/projects/atlax
go test -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go vet ./...
golangci-lint run ./...
make build
wc -l pkg/auth/*.go pkg/agent/*.go internal/config/*.go internal/audit/*.go | sort -n
```

### Exit Criteria

- All tests pass with -race
- Coverage >= 90% for all Phase 2 packages
- All linters pass
- All scaffold interfaces satisfied (compile-time verified)
- Agent binary builds and starts
- End-to-end smoke test passes
- No file exceeds 800 lines, no function exceeds 50 lines
- All 5 step reports written in `docs/development/phase2/`
- Phase 2 completion report written
- Agent binary ready to be tested against Phase 3 relay

---

## Anti-Pattern Checklist

| Anti-Pattern | Mitigation |
|---|---|
| Writing implementation before tests | TDD enforced: test file listed before implementation in every step |
| Hardcoded cert paths in auth code | All paths come from config; tests use temp dirs with generated certs |
| Blocking audit emit | SlogEmitter uses buffered channel; Emit never blocks the caller |
| Reconnection thundering herd | Jitter added to exponential backoff; validated by test |
| Goroutine leak in forwarder | Both copy goroutines terminated when either finishes; verified with -race |
| Half-close not propagated | Forwarder tests verify stream close when local conn closes and vice versa |
| Config validation at wrong layer | Loader validates immediately on load; no invalid config reaches runtime |
| Testing with real network | Tests use net.Pipe() or in-memory TLS for deterministic, fast execution |
| Signal handling race | Shutdown uses context cancellation, not direct goroutine kill |

---

## Plan Mutation Protocol

If requirements change during execution:

1. **Split a step**: Create sub-steps (e.g., 1a, 1b) with clear boundaries
2. **Insert a step**: Add between existing steps, update dependency edges
3. **Skip a step**: Mark as SKIPPED with rationale, verify dependents still work
4. **Reorder**: Only if dependency graph allows (check `Depends on` fields)
5. **Abandon**: Mark as ABANDONED, document reason, clean up any partial work

All mutations must be recorded in this plan file with timestamp and rationale.

---

## Execution Log

| Step | Status | PR | Commit | Date |
|------|--------|----|--------|------|
| Step 1: Protocol + mTLS Auth | COMPLETED | #18 | e693d91 | 2026-03-29 |
| Step 2: Agent Client | COMPLETED | #19 | f50a008 | 2026-03-29 |
| Step 3: Service Forwarder | COMPLETED | #20 | 76d0b0b | 2026-03-29 |
| Step 4: Audit Emitter | COMPLETED | #21 | 8c73300 | 2026-03-29 |
| Step 5: Config + Tunnel + Binary | IN PROGRESS | #22 | bf7c627 | 2026-03-29 |
| Step 6: Integration & Ship | NOT STARTED | -- | -- | -- |

---

*Generated: 2026-03-28*
*Blueprint version: 1.0*
*Objective: Implement tunnel agent binary with mTLS, reconnection, and traffic forwarding (Phase 2)*
*Predecessor: plans/phase1-core-protocol-plan.md (Phase 1, completed 2026-03-23)*
