# Blueprint: Phase 6 -- Operations and Enterprise Separation

**Objective:** Make atlax deployable at scale with infrastructure-as-code, monitoring, zero-downtime updates, and a clean separation between the community (open-source, Apache 2.0) and enterprise (private, commercial) codebases. After this phase, the community edition is distributable and the enterprise edition has its own repo with commercial-only features.

**Status:** IN PROGRESS (Steps 1-5 complete, Steps 6-8 remaining)
**Target duration:** 3-4 weeks
**Estimated sessions:** 8-12

**Related issues:** #32, #33, #40, #60, #61, #62, #63, #64, #65, #66, #67, #68, #69, #70, #71

---

## Scope

### Track A: Operations (community edition)

| Issue | Item | Step |
|-------|------|------|
| #64 | Hardened systemd service files | Step 1 |
| #65 | Production Docker images | Step 1 |
| #70 | Wire per-customer rate limit from YAML | Step 2 |
| #71 | Prometheus setup guide | Step 2 |
| #67 | Grafana monitoring dashboards | Step 2 |
| #68 | Prometheus alerting rules | Step 2 |
| #33 | Fuzz testing for FrameCodec | Step 3 |
| #32 | sync.Pool for Frame objects (data-driven) | Step 3 |
| #60 | Agent-stack benchmark mode | Step 3 |
| #40 | Dynamic port allocation (admin API) | Step 4 |

### Track B: Enterprise features

| Issue | Item | Step |
|-------|------|------|
| #61 | Zero-downtime relay binary swap (fd passing) | Step 6 |
| #62 | Agent self-update via signed manifest | Step 6 |
| #63 | Multi-agent support (max_connections > 1) | Step 7 |
| #69 | Certificate issuance automation | Step 7 |

### Track C: Enterprise separation

| Item | Step |
|------|------|
| Extract enterprise interfaces into stable API contract | Step 8 |
| Create `atlax-enterprise` private repo | Step 8 |
| Enterprise cmd/relay and cmd/agent with commercial wiring | Step 8 |
| Distribution: community tarball, enterprise Docker images | Step 8 |

---

## Enterprise/Community Separation Strategy

### Architecture

The separation uses Go's interface system. No build tags, no conditional compilation. Two separate repos, two separate binaries.

```
github.com/atlasshare/atlax                 (public, Apache 2.0)
    pkg/relay/registry.go                    -> AgentRegistry interface
    pkg/auth/certs.go                        -> CertificateStore interface
    internal/audit/audit.go                  -> Emitter interface
    cmd/relay/main.go                        -> wires MemoryRegistry, FileStore, SlogEmitter
    cmd/agent/main.go                        -> wires FileStore, no self-update

github.com/atlasshare/atlax-enterprise      (private, commercial license)
    go.mod: require github.com/atlasshare/atlax
    pkg/redis/registry.go                    -> RedisRegistry satisfies AgentRegistry
    pkg/vault/certstore.go                   -> VaultStore satisfies CertificateStore
    pkg/siem/emitter.go                      -> SIEMEmitter satisfies Emitter
    pkg/update/updater.go                    -> agent self-update system
    pkg/graceful/restart.go                  -> fd-passing binary swap
    cmd/relay/main.go                        -> wires RedisRegistry, VaultStore, SIEMEmitter
    cmd/agent/main.go                        -> wires VaultStore, self-updater
```

### What stays in community

Everything that exists today. The community edition is fully functional for single-relay, single-agent-per-customer deployments. It includes:

- Full mux protocol with all commands
- mTLS authentication with file-based certs
- Multi-tenant isolation with per-customer limits
- Rate limiting, Prometheus metrics, health check
- Structured audit logging (slog/JSON)
- Reconnection supervision, idle timeout, stream recycling
- Caddy reverse proxy pattern support

### What goes in enterprise

Features that require external infrastructure or commercial support:

- **Distributed registry** (Redis/etcd) -- required for multi-relay clustering
- **SIEM audit integration** -- Splunk, Datadog, ELK event forwarding
- **Vault/step-ca cert store** -- automated cert issuance and rotation via PKI
- **Zero-downtime binary swap** -- fd passing for relay, exec for agent
- **Agent self-update** -- signed manifest, download, verify, restart
- **Multi-agent** -- multiple agent connections per customer with load balancing
- **Fleet management** -- staged rollout, canary deployments, version pinning
- **Web dashboard** -- connection monitoring, customer management UI

### Distribution

| Edition | Binary | License | Distribution |
|---------|--------|---------|-------------|
| Community | `atlax-relay`, `atlax-agent` | Apache 2.0 | GitHub releases (tarball, Docker Hub) |
| Enterprise | `atlax-relay-enterprise`, `atlax-agent-enterprise` | Commercial | Private Docker registry, signed downloads |

The enterprise binaries are drop-in replacements. Same config format, same ports, same protocol. The only difference is which implementations are wired in `main.go`.

### Interface Stability Contract

The community repo's interfaces are the enterprise API contract. Breaking changes to these interfaces break the enterprise build. Rules:

1. Interfaces in `pkg/relay/`, `pkg/auth/`, `internal/audit/` are **stable** after Phase 6
2. Adding methods to an interface is a breaking change (requires enterprise update)
3. New extension points use new interfaces, not method additions
4. The enterprise repo's CI runs `go build` against the community module -- any break is caught immediately

---

## Dependency Graph

```
Step 1 (Systemd + Docker)
   |
   v
Step 2 (Monitoring: rate limit wiring, Prometheus guide, Grafana, alerting)
   |
   v
Step 3 (Security + performance: fuzz, sync.Pool, agent benchmark)
   |
   v
Step 4 (Admin API: dynamic port allocation)
   |
   v
Step 5 (Community release prep: port lifecycle fix, coverage, v0.1.0 tag)
   |
   v
Step 6 (Enterprise: zero-downtime swap + self-update)
   |
   v
Step 7 (Enterprise: multi-agent + cert automation)
   |
   v
Step 8 (Enterprise separation: private repo, distribution)
```

---

## Step 1: Hardened Systemd + Production Docker -- TDD

**Branch:** `phase6/infra`
**Closes:** #64, #65

### Tasks

#### Systemd

- [ ] `deployments/systemd/atlax-relay.service` -- hardened unit with ProtectSystem=strict, NoNewPrivileges, CapabilityBoundingSet, PrivateTmp, ReadOnlyPaths
- [ ] `deployments/systemd/atlax-agent.service` -- same hardening
- [ ] Documentation in `docs/operations/systemd.md`

#### Docker

- [ ] `deployments/docker/Dockerfile.relay` -- multi-stage: Go build -> scratch/distroless, non-root USER, HEALTHCHECK instruction
- [ ] `deployments/docker/Dockerfile.agent` -- same pattern
- [ ] `docker-compose.yml` for local dev (relay + agent + echo server)
- [ ] Documentation in `docs/operations/docker.md`

---

## Step 2: Monitoring Stack -- TDD

**Branch:** `phase6/monitoring`
**Closes:** #67, #68, #70, #71

### Tasks

- [ ] Wire per-customer rate limiter from YAML config into ClientListener
- [ ] `docs/operations/prometheus.md` -- scrape config, retention, example queries
- [ ] `deployments/grafana/relay-dashboard.json` -- Grafana dashboard JSON
- [ ] `deployments/prometheus/alerts.yml` -- alert rules for agent disconnect, rejection spike, relay down
- [ ] Update `docs/operations/setup-and-testing.md` with monitoring section

---

## Step 3: Security + Performance -- TDD

**Branch:** `phase6/security-perf`
**Closes:** #32, #33, #60

### Tasks

- [ ] `FuzzReadFrame` in `pkg/protocol/frame_codec_test.go` -- fuzz the frame decoder
- [ ] Run fuzz for extended period, triage findings
- [ ] Evaluate sync.Pool: run load test with pprof, implement only if Frame allocations dominate
- [ ] Agent-stack benchmark mode in loadtest tool

---

## Step 4: Admin API -- TDD

**Branch:** `phase6/admin-api`
**Closes:** #40

### Tasks

- [ ] `POST /ports` -- add port mapping at runtime
- [ ] `DELETE /ports/{port}` -- remove port mapping
- [ ] `GET /ports` -- list current mappings
- [ ] Authentication on admin API (bearer token or mTLS)
- [ ] ClientListener.StartPort/StopPort for runtime port management

---

## Step 5: Community Release Prep (v0.1.0)

**Branch:** `phase6/port-lifecycle`

See `plans/phase6-step5-community-release-plan.md` for the full sub-step breakdown.

### Tasks

- [x] Fix admin API port lifecycle: POST /ports starts TCP listener, DELETE /ports stops it
- [x] Add StopPort method to ClientListener, ListenAddr field to PortCreateRequest
- [x] Raise pkg/relay coverage from 72% to 88%
- [x] Rewrite stale docs/api/control-plane.md to match actual implementation
- [x] Update phase 6 execution log
- [x] Tag v0.1.0 on main + GitHub release

---

## Step 6: Enterprise -- Zero-Downtime + Self-Update

**Branch:** `phase6/enterprise-updates` (on `atlax-enterprise` repo)
**Depends on:** Step 5 (v0.1.0 tag)
**Closes:** #61, #62

### Tasks

#### Relay binary swap

- [ ] SIGUSR2 handler in relay process
- [ ] Pass listening fds via environment (LISTEN_FDS)
- [ ] New process detects inherited fds, skips bind
- [ ] Old process drains connections and exits
- [ ] Test: swap under load, verify zero dropped connections

#### Agent self-update

- [ ] `pkg/update/updater.go` -- poll manifest, download, verify, exec
- [ ] Manifest format: JSON with version, platform, URL, SHA-256, ed25519 signature
- [ ] ed25519 public key embedded via ldflags at build time
- [ ] Atomic binary replacement (temp file + rename)
- [ ] Test: mock manifest server, verify update cycle

---

## Step 7: Enterprise -- Multi-Agent + Cert Automation

**Branch:** `phase6/enterprise-features` (on `atlax-enterprise` repo)
**Depends on:** Step 6
**Closes:** #63, #69

### Tasks

#### Multi-agent

- [ ] MemoryRegistry: map[string][]*LiveConnection (list per customer)
- [ ] Register: append when under max_connections limit
- [ ] Route: round-robin or least-streams selection across connections
- [ ] Unregister: remove specific connection, not all

#### Certificate automation

- [ ] Integration with step-ca or Vault PKI
- [ ] CSR generation and submission API
- [ ] Automated issuance for new customers
- [ ] Rotation triggered by WatchForRotation

---

## Step 8: Enterprise Separation + Distribution

**Branch:** `phase6/separation` (both repos)
**Depends on:** Steps 6-7

### Tasks

#### Community repo cleanup

- [ ] Audit all interfaces: document stability guarantees in `docs/api/interfaces.md`
- [ ] Remove any enterprise-specific comments or TODO placeholders
- [ ] Tag release: `v0.1.0`
- [ ] GitHub release with pre-built binaries (linux/amd64, linux/arm64, darwin/arm64)
- [ ] Docker Hub push: `atlasshare/atlax-relay:1.0.0`, `atlasshare/atlax-agent:1.0.0`

#### Enterprise repo creation

- [ ] `github.com/atlasshare/atlax-enterprise` (private)
- [ ] `go.mod`: require `github.com/atlasshare/atlax v0.1.0`
- [ ] `cmd/relay/main.go`: wire RedisRegistry + VaultStore + SIEMEmitter
- [ ] `cmd/agent/main.go`: wire VaultStore + self-updater
- [ ] CI: build enterprise binaries, run community test suite
- [ ] Private Docker registry: `registry.atlasshare.io/atlax-relay-enterprise:1.0.0`
- [ ] License file: commercial terms

#### Distribution verification

- [ ] Community binary works standalone (no enterprise deps)
- [ ] Enterprise binary is drop-in replacement (same config, same protocol)
- [ ] Enterprise features activate based on wired implementations (no feature flags)
- [ ] Customer can migrate community -> enterprise by swapping binaries only

---

## Anti-Pattern Checklist

| Anti-Pattern | Mitigation |
|---|---|
| Enterprise code leaks into community repo | Separate repos; community has no import of enterprise module |
| Interface breaking change breaks enterprise | CI in enterprise repo builds against community module |
| Build tags for edition switching | No build tags; two separate main.go files in two repos |
| Feature flags for enterprise | No flags; edition is determined by which binary runs |
| Self-update without signature verification | ed25519 mandatory; public key embedded at compile time |
| fd passing race condition | New process signals readiness before old process stops accepting |

---

## Execution Log

| Step | Status | PR | Date |
|------|--------|----|------|
| Step 1: Systemd + Docker | COMPLETED | #72 | 2026-04-02 |
| Step 2: Monitoring | COMPLETED | #73 | 2026-04-02 |
| Step 3: Security + perf | COMPLETED | #74 | 2026-04-02 |
| Step 4: Admin API | COMPLETED | #75 | 2026-04-03 |
| Step 5: Community release prep (v0.1.0) | COMPLETED | #76 | 2026-04-03 |
| Step 6: Enterprise updates | NOT STARTED | -- | -- |
| Step 7: Enterprise features | NOT STARTED | -- | -- |
| Step 8: Separation + distribution | NOT STARTED | -- | -- |

---

*Generated: 2026-04-02*
*Blueprint version: 1.0*
*Objective: Operations tooling, enterprise features, and codebase separation (Phase 6)*
*Predecessor: plans/phase5-production-hardening-plan.md (Phase 5, completed 2026-04-02)*
