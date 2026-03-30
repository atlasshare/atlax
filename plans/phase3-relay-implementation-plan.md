# Blueprint: Phase 3 -- Relay Implementation

**Objective:** Implement the atlax relay server (`atlax-relay` binary) that accepts agent mTLS connections, listens for client TCP traffic on dedicated per-customer ports, and routes traffic through multiplexed streams to the correct agent. Also resolves three high-priority Phase 2 carry-forward items.

**Status:** ALL STEPS COMPLETE
**Target duration:** 3 weeks
**Estimated sessions:** 5-8 (steps 1-2 serial, steps 3-4 parallelizable, steps 5-6 serial)

**Prerequisites:** Phase 2 complete (merged to main). All prerequisite tasks in `plans/prereqs/phase3/` must be completed by the user before execution begins.

---

## Phase 2 Carry-Forward Items (MUST resolve in Phase 3)

These items are blocking production readiness and must be addressed before any new relay-specific work:

| # | Item | Severity | Resolution Step |
|---|------|----------|-----------------|
| 1 | STREAM_CLOSE wire emission | High | Step 1 |
| 2 | Multi-service routing via STREAM_OPEN payload | High | Step 3 |
| 3 | Reconnection supervision in agent | High | Step 6 (integration) |

---

## Dependency Graph

```
Step 1 (Protocol: STREAM_CLOSE emission + relay config)
   |
   v
Step 2 (Agent Registry + Agent Listener)
   |
   +--> Step 3 (Traffic Router + Client Listener)  ---+
   |                                                    |
   +--> Step 4 (Relay Config Loader validation)        +--> Step 6 (Integration & Ship)
   |                                                    |
   Step 5 (cmd/relay + Graceful Shutdown)  ------------+
```

**Parallelism:** Steps 3 and 4 share no files and can execute concurrently. Step 5 depends on Steps 2 and 3.

---

## Naming Conventions (enforced across ALL steps)

- **New file naming:** Implementation files use descriptive suffixes. Test files use `_test` suffix.
- **Type naming:** Concrete types named directly. No "Impl" suffix. Avoid package-name stutter (e.g., `relay.Server` not `relay.RelayServer`).
- **Error wrapping:** All errors use `fmt.Errorf("component: operation: %w", err)` pattern.
- **Logging:** All production code uses `log/slog` structured logging.
- **Context:** All functions performing I/O accept `context.Context` as first parameter.

---

## Invariants (verified after EVERY step)

1. `go build ./...` passes
2. `go vet ./...` passes
3. `go test -race ./...` passes (ALL packages)
4. `gofmt -l .` returns no output
5. `golangci-lint run ./...` passes
6. Coverage for changed packages >= 90%
7. No function exceeds 50 lines
8. No file exceeds 800 lines
9. No hardcoded secrets or private keys
10. No emoji in code, comments, or test output
11. All new types satisfy existing scaffold interfaces where applicable
12. All I/O functions propagate context.Context
13. **Step report written immediately after each step completes**

---

## Step 1: Protocol Fix -- STREAM_CLOSE Wire Emission -- TDD

**Branch:** `phase3/stream-close`
**Depends on:** Phase 2 (main)
**Model tier:** strongest (protocol correctness is critical)
**Serial:** yes (all relay steps depend on graceful stream close)
**Rollback:** `git branch -D phase3/stream-close`

### Context Brief

Complete the stream close handshake that was deferred from Phase 2. When `Stream.Close()` is called, the MuxSession must detect the local state transition and emit a STREAM_CLOSE+FIN frame on the wire, so the remote peer learns the stream closed gracefully instead of only via Reset (hard abort).

Currently `Stream.Close()` only transitions local state (Open -> HalfClosedLocal, HalfClosedRemote -> Closed). The MuxSession has no hook to detect this and send STREAM_CLOSE. The receiving side already handles incoming STREAM_CLOSE frames correctly (`handleStreamClose` in mux_session.go).

**Reference:** Phase 2 completion report "Open Items Carried Forward", `docs/protocol/stream-lifecycle.md`

**Critical rules:**
- Close must emit STREAM_CLOSE+FIN frame exactly once per stream
- Close must not emit if stream is already in HalfClosedLocal, Closed, or Reset state
- The frame must be enqueued to the write queue, not written directly (concurrency safety)
- Existing tests must continue to pass (no behavioral regression)

### Tasks

#### Test additions: `pkg/protocol/mux_session_test.go`

- [ ] Test stream Close on relay side emits STREAM_CLOSE+FIN that agent receives (agent stream transitions to HalfClosedRemote)
- [ ] Test stream Close on agent side emits STREAM_CLOSE+FIN that relay receives
- [ ] Test double Close does not emit duplicate STREAM_CLOSE frames
- [ ] Test Close after Reset does not emit STREAM_CLOSE
- [ ] Test full lifecycle: relay opens stream, both sides exchange data, relay closes, agent reads EOF, agent closes, stream removed from both sides

#### Implementation changes: `pkg/protocol/stream_impl.go`, `pkg/protocol/mux_session.go`

- [ ] Add `onClose` callback field to StreamSession (set by MuxSession when stream is registered)
- [ ] Stream.Close() calls `onClose(streamID)` after state transition to HalfClosedLocal or Closed
- [ ] MuxSession.registerStream sets onClose to enqueue STREAM_CLOSE+FIN frame
- [ ] Guard: onClose is called at most once (use sync.Once or state check)

Also in this step -- update `internal/config/` for relay config:

#### Config updates: `internal/config/config.go`, `internal/config/loader.go`

- [ ] Update `CustomerConfig` to match relay.example.yaml (id, ports with service/description)
- [ ] Add `LoadRelayConfig` validation (listen_addr, tls paths, at least one customer)
- [ ] Add relay-specific env var overrides (ATLAX_LISTEN_ADDR, ATLAX_TLS_CERT, etc.)
- [ ] Tests for relay config loading and validation

### Verification

```bash
go test -race -v -count=3 ./pkg/protocol/... -run "TestStreamClose|TestLifecycle"
go test -race -v ./internal/config/...
go test -race -coverprofile=coverage.out ./pkg/protocol/... ./internal/config/...
go vet ./...
golangci-lint run ./...
```

### Exit Criteria

- STREAM_CLOSE+FIN emitted on Stream.Close() and received by peer
- Full stream lifecycle works end-to-end (open, data, close, EOF, cleanup)
- Relay config loads and validates from YAML
- Coverage >= 90% for changed files
- Step report written in `docs/development/phase3/step1-stream-close-report.md`
- PR: `phase3/stream-close` -> `main`

---

## Step 2: Agent Registry + Agent Listener -- TDD

**Branch:** `phase3/agent-registry`
**Depends on:** Step 1
**Model tier:** strongest (concurrent agent management is correctness-critical)
**Serial:** yes (traffic router depends on registry)
**Rollback:** `git branch -D phase3/agent-registry`

### Context Brief

Implement the community edition AgentRegistry (in-memory map) and the relay's TLS listener that accepts agent mTLS connections. When an agent connects, the relay performs the mTLS handshake, extracts the customer identity via `ExtractIdentity`, creates a MuxSession(RoleRelay), and registers the connection in the registry.

The scaffold `AgentRegistry` and `AgentConnection` interfaces in `pkg/relay/registry.go` define the contract.

**Security reference:** `docs/security/mtls.md`, `docs/security/trust-zones.md`

**Critical rules:**
- One active connection per customer ID (new connection replaces old with GOAWAY)
- Registry operations must be goroutine-safe (concurrent agent connects)
- ExtractIdentity must succeed before registration (reject invalid certs)
- Audit events: auth.success/auth.failure, agent.connected/agent.disconnected

### Tasks

#### AgentConnection concrete type: `pkg/relay/connection.go`

- [ ] `type LiveConnection struct` -- satisfies AgentConnection interface
- [ ] Wraps: tls.Conn, MuxSession, Identity, timestamps
- [ ] Compile-time interface check

#### Agent Registry: `pkg/relay/registry_impl.go`

- [ ] `type MemoryRegistry struct` -- in-memory map[customerID]*LiveConnection, RWMutex
- [ ] Register: stores connection, closes previous if exists (GOAWAY + close)
- [ ] Unregister: removes and closes connection
- [ ] Lookup: returns connection or error
- [ ] Heartbeat: updates lastSeen timestamp
- [ ] ListConnectedAgents: returns snapshot of all connections
- [ ] Compile-time interface check

#### Agent Listener: `pkg/relay/listener.go`

- [ ] `type AgentListener struct` -- accepts TLS connections on agent listen addr
- [ ] `func (l *AgentListener) Start(ctx context.Context) error` -- accept loop
- [ ] For each connection: mTLS handshake, ExtractIdentity, create MuxSession(RoleRelay), register in registry
- [ ] Reject connections with invalid certs (audit auth.failure)
- [ ] Respect MaxAgents limit
- [ ] Emit audit events for connect/disconnect

#### Tests

- [ ] Test MemoryRegistry Register/Lookup/Unregister lifecycle
- [ ] Test Register replaces existing connection with GOAWAY
- [ ] Test Lookup returns error for unknown customer
- [ ] Test Heartbeat updates LastSeen
- [ ] Test ListConnectedAgents returns all connections
- [ ] Test concurrent Register/Unregister (goroutine safety)
- [ ] Test AgentListener accepts mTLS connection and registers (integration with dev certs)
- [ ] Test AgentListener rejects connection with wrong CA
- [ ] Test AgentListener respects MaxAgents limit

### Verification

```bash
go test -race -v -count=3 ./pkg/relay/...
go test -race -coverprofile=coverage.out ./pkg/relay/...
go tool cover -func=coverage.out | grep relay
go vet ./...
golangci-lint run ./...
```

### Exit Criteria

- Agent connects over mTLS, identity extracted, registered in registry
- Duplicate connections replaced gracefully (GOAWAY to old)
- Registry is goroutine-safe under concurrent access
- Coverage >= 90% for registry_impl.go, connection.go, listener.go
- Step report written in `docs/development/phase3/step2-registry-listener-report.md`
- PR: `phase3/agent-registry` -> `main`

---

## Step 3: Traffic Router + Client Listener -- TDD

**Branch:** `phase3/traffic-router`
**Depends on:** Step 2
**Model tier:** strongest (routing correctness is security-critical -- cross-tenant isolation)
**Parallel with:** Step 4
**Rollback:** `git branch -D phase3/traffic-router`

### Context Brief

Implement the TrafficRouter that maps incoming client TCP connections to the correct agent stream, and the Client Listener that accepts plain TCP on per-customer dedicated ports.

Data flow: Client connects to relay port 8080 -> relay looks up which customer owns port 8080 -> relay opens a stream on that customer's MuxSession -> relay copies data bidirectionally between client TCP and the mux stream.

This step also resolves the Phase 2 carry-forward item: **multi-service routing**. The STREAM_OPEN frame payload carries the service name so the agent can route to the correct local service.

**Security reference:** `docs/security/trust-zones.md` (Zone 1: Client is untrusted)

**Critical rules:**
- Port-to-customer mapping is static (from config) -- no dynamic allocation in Phase 3
- Client connections are plain TCP (no TLS) -- the tunnel provides encryption
- STREAM_OPEN payload must include the service name for the agent to route
- Cross-tenant isolation: a client on customer A's port must never reach customer B's agent
- Rate limiting per source IP is deferred to Phase 4

### Tasks

#### Traffic Router: `pkg/relay/router_impl.go`

- [ ] `type PortRouter struct` -- satisfies TrafficRouter interface
- [ ] Port-to-customer-service map built from config at startup
- [ ] Route: lookup customer by port, get AgentConnection from registry, open stream with service name in payload, bidirectional copy
- [ ] AddPortMapping / RemovePortMapping for dynamic updates
- [ ] Compile-time interface check

#### Client Listener: `pkg/relay/client_listener.go`

- [ ] `type ClientListener struct` -- listens on per-customer TCP ports
- [ ] Starts one TCP listener per configured port
- [ ] For each client connection: resolve customer+service from port, call router.Route
- [ ] Connection limits per port (from config)

#### STREAM_OPEN payload: `pkg/protocol/`

- [ ] Define STREAM_OPEN payload format: service name as UTF-8 string
- [ ] Update MuxSession.OpenStream to accept optional payload (target service name)
- [ ] Update agent's TunnelRunner.resolveTarget to parse STREAM_OPEN payload
- [ ] Tests for payload encoding/decoding

#### Tests

- [ ] Test PortRouter routes client to correct agent via stream
- [ ] Test PortRouter rejects connection for unregistered customer
- [ ] Test PortRouter rejects connection for port not in allocation
- [ ] Test STREAM_OPEN payload carries service name
- [ ] Test agent resolveTarget parses service name from stream
- [ ] Test end-to-end: client -> relay port -> stream -> agent -> local echo server -> response back
- [ ] Test cross-tenant isolation: client on port A cannot reach agent B
- [ ] Test ClientListener starts/stops per-port listeners

### Verification

```bash
go test -race -v -count=3 ./pkg/relay/... -run "TestRouter|TestClientListener"
go test -race -v ./pkg/protocol/... -run TestStreamOpen
go test -race -v ./pkg/agent/... -run TestTunnel
go test -race -coverprofile=coverage.out ./pkg/relay/... ./pkg/protocol/... ./pkg/agent/...
go vet ./...
golangci-lint run ./...
```

### Exit Criteria

- Client TCP -> relay -> stream -> agent -> local service -> response works end-to-end
- Service name carried in STREAM_OPEN payload and parsed by agent
- Cross-tenant isolation verified
- Coverage >= 90% for router_impl.go, client_listener.go
- Step report written in `docs/development/phase3/step3-traffic-router-report.md`
- PR: `phase3/traffic-router` -> `main`

---

## Step 4: Relay Config Loader Validation -- TDD

**Branch:** `phase3/relay-config`
**Depends on:** Step 1 (config struct updates)
**Model tier:** default (config validation is straightforward)
**Parallel with:** Step 3
**Rollback:** `git branch -D phase3/relay-config`

### Context Brief

Expand the config loader with relay-specific validation, the `CustomerConfig` struct to match the YAML format with port/service mappings, and relay environment variable overrides.

The relay config YAML uses a different `customers` structure than what Phase 2 defined -- each customer has an `id` and a list of `ports` with `port`, `service`, and `description`. The Go struct must match.

### Tasks

#### Config struct updates: `internal/config/config.go`

- [ ] Update `CustomerConfig` to include `Ports []PortConfig` with port/service/description
- [ ] Add `PortConfig` struct with yaml tags
- [ ] Add `AdminAddr` to `ServerConfig` (from relay.example.yaml)

#### Loader updates: `internal/config/loader.go`

- [ ] `validateRelayConfig`: listen_addr, tls cert/key/client_ca, at least one customer
- [ ] Relay env overrides: ATLAX_LISTEN_ADDR, ATLAX_AGENT_LISTEN_ADDR, ATLAX_TLS_CERT, ATLAX_TLS_KEY, ATLAX_TLS_CA, ATLAX_TLS_CLIENT_CA
- [ ] Build port-to-customer index from config (utility function for router)

#### Tests

- [ ] Test LoadRelayConfig with valid YAML matching relay.example.yaml
- [ ] Test validation rejects missing listen_addr, TLS paths, empty customers
- [ ] Test env var overrides for relay
- [ ] Test port-to-customer index builder

### Verification

```bash
go test -race -v ./internal/config/...
go test -race -coverprofile=coverage.out ./internal/config/...
```

### Exit Criteria

- Relay config loads from relay.example.yaml format
- Validation catches missing required fields
- Coverage >= 90% for config package
- Step report written in `docs/development/phase3/step4-relay-config-report.md`
- PR: `phase3/relay-config` -> `main`

---

## Step 5: cmd/relay + Graceful Shutdown -- TDD

**Branch:** `phase3/relay-binary`
**Depends on:** Steps 2, 3, 4
**Model tier:** default (wiring layer)
**Serial:** yes (integrates all prior steps)
**Rollback:** `git branch -D phase3/relay-binary`

### Context Brief

Wire all relay components into the `cmd/relay/main.go` binary. The relay:
1. Loads config
2. Initializes mTLS (server-side: relay cert + customer CA for client auth)
3. Creates AgentRegistry, AgentListener, TrafficRouter, ClientListener
4. Starts AgentListener (accepts agents) and ClientListener (accepts clients)
5. Waits for SIGINT/SIGTERM
6. Sends GOAWAY to all agents, drains connections with timeout, shuts down

**Critical rules:**
- Graceful shutdown: GOAWAY to all agents, wait for active streams to drain, then force-close
- ShutdownGracePeriod from config controls the drain timeout
- Audit events on startup, shutdown, agent connect/disconnect

### Tasks

#### cmd/relay/main.go

- [ ] `run()` pattern (same as cmd/agent)
- [ ] Load relay config via FileLoader
- [ ] Initialize slog logger
- [ ] Create audit SlogEmitter
- [ ] Build server-side mTLS config via Configurator.ServerTLSConfig
- [ ] Create MemoryRegistry
- [ ] Create AgentListener (bind to agent listen addr)
- [ ] Create PortRouter (from config port mappings)
- [ ] Create ClientListener (bind to per-customer ports)
- [ ] Start listeners
- [ ] Signal handling (SIGINT, SIGTERM)
- [ ] Graceful shutdown: GOAWAY all agents, stop listeners, drain, close

#### Relay Server facade: `pkg/relay/server_impl.go`

- [ ] `type RelayServer struct` -- satisfies Server interface
- [ ] Orchestrates AgentListener + ClientListener + Registry
- [ ] Start/Stop lifecycle
- [ ] Addr returns client-facing listener address

#### Tests

- [ ] Test RelayServer Start/Stop lifecycle
- [ ] Test graceful shutdown sends GOAWAY to registered agents
- [ ] Test shutdown respects grace period timeout

### Verification

```bash
go test -race -v ./pkg/relay/...
go build ./cmd/relay/...
go vet ./...
golangci-lint run ./...
```

### Exit Criteria

- `go build ./cmd/relay/` produces working binary
- Graceful shutdown with GOAWAY and drain timeout
- RelayServer satisfies Server interface
- Step report written in `docs/development/phase3/step5-relay-binary-report.md`
- PR: `phase3/relay-binary` -> `main`

---

## Step 6: Integration Verification and Ship

**Branch:** `main` (all branches merged)
**Depends on:** Steps 1-5
**Model tier:** default
**Serial:** yes (final gate)
**Rollback:** N/A (verification only)

### Context Brief

Final verification after all Phase 3 PRs are merged. Full end-to-end test: start relay, start agent, connect client, verify traffic flows. Also resolve the Phase 2 carry-forward: **reconnection supervision** (agent reconnects automatically when relay restarts).

### Merge Strategy

Merge branches in dependency order:
1. `phase3/stream-close` (foundation)
2. `phase3/agent-registry` (depends on 1)
3. `phase3/traffic-router` (depends on 2)
4. `phase3/relay-config` (independent)
5. `phase3/relay-binary` (depends on 2, 3, 4)

### Tasks

- [ ] Merge all PRs in order
- [ ] Run full test suite: `go test -race -coverprofile=coverage.out ./...`
- [ ] Verify overall coverage >= 90% for all Phase 3 packages
- [ ] Run `go vet ./...`, `golangci-lint run ./...`, `gofmt -l .`
- [ ] Build both binaries: `make build`
- [ ] Verify all scaffold interfaces satisfied:
  - `Server` satisfied by `RelayServer`
  - `AgentRegistry` satisfied by `MemoryRegistry`
  - `AgentConnection` satisfied by `LiveConnection`
  - `TrafficRouter` satisfied by `PortRouter`
- [ ] **End-to-end smoke test:**
  - Start relay with dev certs and one customer port mapping
  - Start agent with dev certs pointing to relay
  - Connect plain TCP client to relay port
  - Send data, verify it arrives at agent's local echo server
  - Verify response flows back to client
  - Kill relay, verify agent detects and reconnects when relay restarts
  - Verify graceful shutdown: SIGTERM relay, verify GOAWAY sent, streams drain
- [ ] Verify no hardcoded secrets, no emoji, no function > 50 lines, no file > 800 lines

### Post-Green Documentation

Write Phase 3 completion report in `docs/development/phase3/phase3-completion-report.md`:

- [ ] **Phase summary:** What Phase 3 delivered, total files, tests, coverage
- [ ] **Consolidated issue log**
- [ ] **Consolidated decision log**
- [ ] **Open items carried forward:** Must include ALL of the following:
  - **From Phase 1 (deferred through Phase 2 and Phase 3):**
    - Stream ID exhaustion / recycling (deferred to Phase 5)
    - sync.Pool for Frame objects (deferred to Phase 5)
    - Fuzz testing for FrameCodec (deferred to security phase)
  - **From Phase 2 (deferred through Phase 3):**
    - IdleTimeout for Forwarder (deferred to Phase 5)
    - Full cert rotation with CSR/sign cycle (deferred to Phase 5)
  - **From Phase 3 (new for Phase 4):**
    - Per-customer rate limiting
    - Per-customer connection limits
    - Per-customer stream limits enforcement
    - Dynamic port allocation
    - Any new items discovered during Phase 3
- [ ] **Architecture snapshot**
- [ ] **Performance baseline**
- [ ] **Lessons learned**

### Exit Criteria

- End-to-end traffic flows: client -> relay -> agent -> local -> agent -> relay -> client
- Agent reconnects after relay restart
- Graceful shutdown works
- All CI jobs green
- Phase 3 completion report written
- Both binaries production-ready for single-tenant deployment

---

## Anti-Pattern Checklist

| Anti-Pattern | Mitigation |
|---|---|
| Writing implementation before tests | TDD enforced: test file listed before implementation in every step |
| Cross-tenant routing | PortRouter validates customer ID matches port allocation before routing |
| Agent impersonation | mTLS with ExtractIdentity; reject connections with invalid/unknown certs |
| Stale registry entries | AgentListener detects disconnect (read loop EOF) and calls Unregister |
| Port conflict on startup | ClientListener validates all ports are bindable before starting accept loops |
| Goroutine leak in router | Bidirectional copy uses context cancellation; copy goroutines exit when context canceled |
| Blocking on agent connect | AgentListener runs each connection in a separate goroutine |
| Documentation gap | Step reports written immediately after each step (invariant #13) |

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

## Execution Log

| Step | Status | PR | Date |
|------|--------|----|------|
| Step 1: STREAM_CLOSE emission | COMPLETED | #25 | 2026-03-29 |
| Step 2: Agent Registry + Listener | COMPLETED | #26 | 2026-03-29 |
| Step 3: Traffic Router + STREAM_OPEN | COMPLETED | #27 | 2026-03-29 |
| Step 4: Relay Config | COMPLETED | #28 | 2026-03-29 |
| Step 5: cmd/relay + Shutdown | COMPLETED | #29 | 2026-03-29 |
| Step 6: Integration & Ship | COMPLETED | #30 | 2026-03-29 |

---

*Generated: 2026-03-29*
*Blueprint version: 1.0*
*Objective: Implement relay server with agent registry, traffic routing, and client listeners (Phase 3)*
*Predecessor: plans/phase2-agent-implementation-plan.md (Phase 2, completed 2026-03-29)*
