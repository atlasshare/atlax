# Blueprint: Phase 4 -- Multi-tenancy

**Objective:** Harden the relay for multi-tenant operation. Enforce per-customer resource isolation (stream limits, connection limits, rate limiting), add cross-tenant isolation verification, and introduce per-customer metrics. This phase transforms the relay from a single-tenant dev tool into a multi-tenant-ready service.

**Status:** ALL STEPS COMPLETE
**Target duration:** 1-2 weeks
**Estimated sessions:** 3-5 (steps 1-3 serial, step 4 parallel with 3)

**Prerequisites:** Phase 3 complete (merged to main). Live testing recommended before starting.

**Related issues:** #37, #38, #39, #40, #41, #42

---

## Scope Decisions

### In scope (Phase 4)

| Item | Issue | What |
|------|-------|------|
| Per-customer stream limits | #39 | PortRouter.Route checks `mux.NumStreams()` against CustomerConfig.MaxStreams before opening |
| Per-customer connection limits | #38 | MemoryRegistry enforces max connections per customer (default 1, configurable) |
| Per-customer rate limiting | #37 | Token bucket per source IP per customer port using `golang.org/x/time/rate` |
| Cross-tenant isolation test | #42 | Dedicated integration test proving port-A cannot reach agent-B |
| Per-customer metrics | -- | Prometheus counters: streams, bytes, connections, errors per customer_id label |

### Deferred (not Phase 4)

| Item | Issue | Why |
|------|-------|-----|
| Dynamic port allocation | #40 | Requires admin API design; not blocking multi-tenancy |
| Health check endpoint | #41 | Useful but not a tenancy concern |
| Routing by subdomain | -- | Requires SNI-based routing or HTTP host header; defer to Phase 5+ |

---

## Dependency Graph

```
Step 1 (Per-customer limits: streams + connections)
   |
   v
Step 2 (Rate limiting per source IP)
   |
   +--> Step 3 (Cross-tenant isolation test + metrics)
   |
   +--> Step 4 (Relay config updates + docs)  [parallel with 3]
   |
   Step 5 (Integration & Ship)
```

---

## Naming Conventions

Same as Phase 3. All new code uses `log/slog`, `context.Context`, `fmt.Errorf("component: %w", err)`.

---

## Invariants (verified after EVERY step)

1. `go build ./...` passes
2. `go vet ./...` passes
3. `go test -race ./...` passes
4. `gofmt -l .` returns no output
5. `golangci-lint run ./...` passes
6. Coverage for changed packages >= 90%
7. No function exceeds 50 lines, no file exceeds 800 lines
8. No hardcoded secrets, no emoji
9. **Step report written immediately after each step**

---

## Step 1: Per-Customer Stream and Connection Limits -- TDD

**Branch:** `phase4/customer-limits`
**Depends on:** Phase 3 (main)
**Model tier:** default
**Serial:** yes (rate limiting builds on this)
**Rollback:** `git branch -D phase4/customer-limits`

### Context Brief

Enforce two resource limits that exist in config but are not enforced at runtime:

1. **Per-customer stream limits** (`CustomerConfig.MaxStreams`): `PortRouter.Route` must check `mux.NumStreams()` against the customer's configured limit before opening a new stream. If exceeded, reject the client connection with an error.

2. **Per-customer connection limits**: `MemoryRegistry.Register` must enforce a max connection count per customer. Default is 1 (current behavior: replace). Configurable per customer for multi-agent deployments.

### Tasks

#### Stream limits: `pkg/relay/router_impl.go`

- [ ] PortRouter gains a reference to customer config (or a limit lookup function)
- [ ] Route checks `mux.NumStreams()` before `OpenStreamWithPayload`
- [ ] If limit exceeded, close client connection with error log
- [ ] New sentinel error: `ErrStreamLimitExceeded`

#### Connection limits: `pkg/relay/registry_impl.go`

- [ ] Add `maxConnsPerCustomer` field to MemoryRegistry (or per-customer via config)
- [ ] Register checks count before inserting
- [ ] If limit is 1 (default): existing replace-with-GOAWAY behavior
- [ ] If limit > 1: allow N concurrent connections per customer
- [ ] New sentinel error: `ErrConnectionLimitExceeded`

#### Config: `internal/config/config.go`

- [ ] Add `MaxConnections` to `CustomerConfig` (default: 1)
- [ ] Ensure `MaxStreams` default is sensible (100 if not set)

#### Tests

- [ ] Test Route rejects when stream limit exceeded
- [ ] Test Route allows when under limit
- [ ] Test Register enforces connection limit
- [ ] Test Register with limit > 1 allows multiple connections
- [ ] Test Register with limit 1 replaces (existing behavior preserved)

### Exit Criteria

- Stream limit enforced at routing time
- Connection limit enforced at registration time
- Existing tests pass (no regression)
- Coverage >= 90% for changed files
- Step report written
- PR merged

---

## Step 2: Per-Source-IP Rate Limiting -- TDD

**Branch:** `phase4/rate-limiting`
**Depends on:** Step 1
**Model tier:** default
**Serial:** yes
**Rollback:** `git branch -D phase4/rate-limiting`

### Context Brief

Add a token bucket rate limiter per source IP on each customer port. When a client connects, the rate limiter checks if the source IP has capacity. If not, the connection is rejected immediately (TCP RST or close).

Uses `golang.org/x/time/rate` for the token bucket implementation.

### Tasks

#### Rate limiter: `pkg/relay/ratelimit.go`

- [ ] `type IPRateLimiter struct` -- map of source IP to `*rate.Limiter`, with cleanup
- [ ] `func NewIPRateLimiter(rps float64, burst int) *IPRateLimiter`
- [ ] `func (r *IPRateLimiter) Allow(ip string) bool`
- [ ] Background goroutine to clean up stale entries (IPs not seen for > 10 minutes)

#### Integration: `pkg/relay/client_listener.go`

- [ ] ClientListener gains an IPRateLimiter per port (or shared)
- [ ] `handleClient` checks `rateLimiter.Allow(remoteIP)` before routing
- [ ] If rejected: close connection, log warning with source IP

#### Config: `internal/config/config.go`

- [ ] Add `RateLimit` to `CustomerConfig` or `PortConfig`:
  - `requests_per_second: 100`
  - `burst: 50`

#### Prereq: Add dependency

- [ ] `go get golang.org/x/time`

#### Tests

- [ ] Test Allow returns true under limit
- [ ] Test Allow returns false when exceeded
- [ ] Test different IPs have independent limits
- [ ] Test stale entry cleanup
- [ ] Test ClientListener rejects when rate limited

### Exit Criteria

- Rate limiting enforced per source IP per port
- Config-driven rate/burst values
- Stale IP entries cleaned up
- Coverage >= 90%
- Step report written
- PR merged

---

## Step 3: Cross-Tenant Isolation Test + Per-Customer Metrics -- TDD

**Branch:** `phase4/isolation-metrics`
**Depends on:** Steps 1, 2
**Model tier:** default
**Parallel with:** Step 4
**Rollback:** `git branch -D phase4/isolation-metrics`

### Context Brief

Two deliverables:

**Cross-tenant isolation test** (closes #42):
Dedicated integration test with two customers, two agents, two port sets. Verifies that client on customer-A's port reaches agent-A, and cannot reach agent-B even if both are connected.

**Per-customer Prometheus metrics**:
Add counters and gauges per customer_id label:
- `atlax_streams_total{customer_id}` -- counter
- `atlax_streams_active{customer_id}` -- gauge
- `atlax_connections_total{customer_id}` -- counter
- `atlax_connections_active{customer_id}` -- gauge
- `atlax_client_connections_rejected_total{customer_id, reason}` -- counter (rate_limited, stream_limit, no_agent)

### Tasks

#### Isolation test: `pkg/relay/isolation_test.go`

- [ ] Set up two customers with different port mappings
- [ ] Register two agents (customer-A, customer-B) via pipe-connected mux pairs
- [ ] Client on port-A: verify data reaches agent-A's echo server
- [ ] Client on port-A: verify it does NOT reach agent-B
- [ ] Agent-A disconnects: client on port-A gets error (not routed to agent-B)

#### Metrics: `pkg/relay/metrics.go`

- [ ] `type Metrics struct` -- wraps Prometheus counters/gauges
- [ ] `func NewMetrics(prefix string) *Metrics`
- [ ] Methods: `StreamOpened(customerID)`, `StreamClosed(customerID)`, `ConnectionRegistered(customerID)`, `ClientRejected(customerID, reason)`
- [ ] Integrate into PortRouter.Route and MemoryRegistry.Register

#### Tests

- [ ] Test isolation (described above)
- [ ] Test metrics increment correctly

### Exit Criteria

- Cross-tenant isolation proven by test
- Prometheus metrics exposed per customer
- Coverage >= 90%
- Step report written
- PR merged

---

## Step 4: Per-Port ListenAddr + Config + Documentation -- TDD

**Branch:** `phase4/config-docs`
**Depends on:** Step 1 (config struct changes)
**Model tier:** default
**Parallel with:** Step 3
**Rollback:** `git branch -D phase4/config-docs`

### Context Brief

Three deliverables:

**Per-port ListenAddr**: Add `ListenAddr` field to `PortConfig`. Default `0.0.0.0` (direct exposure). Set to `127.0.0.1` for reverse proxy deployments where Caddy/nginx handles TLS termination and subdomain routing on the edge. This is a deployment knob, not an architecture change -- atlax remains a transport layer.

Production topology with Caddy:
```
Internet -> Caddy (443, TLS, subdomain routing) -> 127.0.0.1:PORT -> relay -> tunnel -> agent
```

**Config updates**: Update `relay.example.yaml` with all Phase 4 fields (max_connections, rate_limit, max_streams, listen_addr per port).

**Documentation**: Multi-tenancy guide explaining isolation guarantees, Caddy integration, and deployment patterns.

### Tasks

#### Per-port ListenAddr: `internal/config/config.go`, `pkg/relay/server_impl.go`

- [ ] Add `ListenAddr string \`yaml:"listen_addr"\`` to `PortConfig` (default: `0.0.0.0`)
- [ ] Update `Relay.Start` to use `entry.ListenAddr:port` instead of `:port`
- [ ] Update `BuildPortIndex` to carry ListenAddr through PortIndexEntry
- [ ] Tests for custom listen_addr in config loading and server startup

#### Config and docs

- [ ] Update `configs/relay.example.yaml` with all new fields and comments
- [ ] Update `docs/operations/setup-and-testing.md` with multi-tenant and Caddy examples
- [ ] Add `docs/operations/multi-tenancy.md` explaining isolation guarantees
- [ ] Tests for config loading with new fields

### Exit Criteria

- Per-port ListenAddr works (customer ports bindable to 127.0.0.1)
- Example config reflects all Phase 4 features
- Documentation explains isolation model and Caddy pattern
- Coverage >= 90%
- Step report written
- PR merged

---

## Step 5: Integration Verification and Ship

**Branch:** `main` (all merged)
**Depends on:** Steps 1-4
**Serial:** yes (final gate)

### Merge Strategy

1. `phase4/customer-limits` (foundation)
2. `phase4/rate-limiting` (depends on 1)
3. `phase4/isolation-metrics` (depends on 1, 2)
4. `phase4/config-docs` (independent)

### Tasks

- [ ] Merge all PRs
- [ ] Full test suite with -race
- [ ] Verify coverage
- [ ] Build both binaries
- [ ] Close issues: #37, #38, #39, #42

### Post-Green Documentation

Phase 4 completion report in `docs/development/phase4/phase4-completion-report.md`:

- [ ] Phase summary, delivery stats
- [ ] Issue log, decision log
- [ ] Open items carried forward (must include all Phase 1-3 deferred items: #31-#36, #40, #41)
- [ ] Coverage report
- [ ] Lessons learned

---

## Anti-Pattern Checklist

| Anti-Pattern | Mitigation |
|---|---|
| Rate limiter memory leak | Background cleanup goroutine for stale IP entries |
| Stream limit checked after open | Check BEFORE OpenStreamWithPayload, not after |
| Metrics cardinality explosion | customer_id is bounded (configured customers only); source IP is NOT a label |
| Cross-tenant test flaky | Use deterministic pipe-based mux pairs, no real network |
| Blocking rate limiter | Use `Allow()` (non-blocking), not `Wait()` |

---

## Plan Mutation Protocol

Same as Phase 3. All mutations recorded with timestamp and rationale.

---

## Execution Log

| Step | Status | PR | Date |
|------|--------|----|------|
| Step 1: Customer limits | COMPLETED | #47 | 2026-04-01 |
| Step 2: Rate limiting | COMPLETED | #48 | 2026-04-01 |
| Step 3: Isolation + metrics | COMPLETED | #49 | 2026-04-01 |
| Step 4: Config + docs | COMPLETED | #50 | 2026-04-01 |
| Step 5: Integration & Ship | COMPLETED | #51 | 2026-04-01 |

---

*Generated: 2026-03-29*
*Blueprint version: 1.0*
*Objective: Multi-tenant relay with per-customer isolation, limits, and metrics (Phase 4)*
*Predecessor: plans/phase3-relay-implementation-plan.md (Phase 3, completed 2026-03-29)*
