# Blueprint: Phase 6, Steps 6-8 -- Enterprise Repo Setup, Zero-Downtime Features, and Distributed Infrastructure

**Objective:** Initialize the `atlax-enterprise` private repository with commercial-only features: zero-downtime relay binary swap, agent self-update, Redis-backed agent registry, Vault/step-ca certificate automation, and SIEM event forwarding. After these steps, the enterprise edition exists as a separate codebase that imports the community module and replaces in-memory/file-based implementations with distributed, production-grade alternatives.

**Status:** NOT STARTED
**Target duration:** 3-4 weeks
**Estimated sessions:** 8-12

**Prerequisites:**
- Phase 6 Steps 1-5 complete (PRs #72-#76 merged to main)
- Community `v0.1.0` tag exists on `github.com/atlasshare/atlax`
- `go get github.com/atlasshare/atlax@v0.1.0` resolves successfully

**Related issues:** #61, #62, #63, #69

---

## Scope

### In scope

| Issue | Item | Step |
|-------|------|------|
| -- | Enterprise repo initialization (`atlax-enterprise`) | Step 6 |
| -- | Community interface stability contract (`docs/api/interfaces.md`) | Step 6 |
| -- | `go.work` workspace setup for local development | Step 6 |
| -- | Enterprise CI/CD (GitHub Actions) | Step 6 |
| #61 | Zero-downtime relay binary swap (SIGUSR2, fd passing) | Step 7a |
| #62 | Agent self-update (signed manifest, ed25519, atomic replace, rollback) | Step 7b |
| #63 | RedisRegistry: multi-agent, multi-relay, round-robin routing | Step 8a |
| #69 | VaultStore: Vault/step-ca cert automation, CSR generation | Step 8b |
| -- | SIEMEmitter: Kafka/NATS event bus for audit forwarding | Step 8c |

### Deferred (not these steps)

| Item | Why |
|------|-----|
| Web dashboard for connection monitoring | Separate project; depends on Steps 8a-8c |
| Fleet management CLI (`ats` enterprise mode) | Requires enterprise admin API (TCP + bearer token) |
| Staged rollout / canary deployments | Depends on fleet management + RedisRegistry |
| RBAC for admin operations | Depends on enterprise admin API |
| Pre-built release binaries (community) | Separate release automation concern |
| Docker Hub push (community) | Separate release automation concern |

### What stays community / what goes enterprise

| Capability | Community (Apache 2.0) | Enterprise (commercial) |
|------------|----------------------|------------------------|
| Agent registry | `MemoryRegistry` (in-process `sync.RWMutex` map) | `RedisRegistry` (Redis hash with TTL, cross-relay lookup) |
| Certificate store | `FileStore` (PEM files on disk, poll-based rotation) | `VaultStore` (Vault PKI/step-ca, CSR submission, automated issuance) |
| Audit emitter | `SlogEmitter` (structured JSON via `log/slog`) | `SIEMEmitter` (Kafka/NATS event bus) |
| Binary lifecycle | Restart required for updates | Relay: zero-downtime fd passing (SIGUSR2). Agent: self-update with crash rollback |
| Relay topology | Single instance | Active-active with shared registry and load balancer |
| Multi-agent | One connection per customer (replace on reconnect) | Multiple connections per customer with round-robin selection |

---

## Enterprise/Community Separation Strategy

### Architecture

Two repos, two binaries, one protocol. No build tags, no conditional compilation. The enterprise repo imports the community module and wires different implementations into the same `main.go` structure.

```
github.com/atlasshare/atlax                    (public, Apache 2.0)
  pkg/relay/registry.go                        AgentRegistry interface
  pkg/relay/registry_impl.go                   MemoryRegistry (community impl)
  pkg/relay/router.go                          TrafficRouter interface
  pkg/relay/server.go                          Server interface
  pkg/auth/certs.go                            CertificateStore interface
  pkg/auth/certs_impl.go                       FileStore (community impl)
  pkg/auth/mtls.go                             TLSConfigurator interface, Configurator impl
  internal/audit/audit.go                      Emitter interface, Event, Action types
  internal/audit/emitter.go                    SlogEmitter (community impl)
  internal/config/config.go                    Config types (shared by both editions)
  cmd/relay/main.go                            Wires MemoryRegistry, FileStore, SlogEmitter
  cmd/agent/main.go                            Wires FileStore, no self-update
  docs/api/interfaces.md                       Interface stability contract (NEW in Step 6)

github.com/atlasshare/atlax-enterprise         (private, commercial license)
  go.mod                                       require github.com/atlasshare/atlax v0.1.0
  pkg/redis/registry.go                        RedisRegistry satisfies AgentRegistry
  pkg/redis/registry_test.go                   RedisRegistry tests (miniredis)
  pkg/vault/certstore.go                       VaultStore satisfies CertificateStore
  pkg/vault/certstore_test.go                  VaultStore tests (mock Vault client)
  pkg/siem/emitter.go                          SIEMEmitter satisfies Emitter
  pkg/siem/emitter_test.go                     SIEMEmitter tests (in-memory broker)
  pkg/graceful/restart.go                      Fd-passing relay binary swap
  pkg/graceful/restart_test.go                 Binary swap tests
  pkg/update/updater.go                        Agent self-update system
  pkg/update/manifest.go                       VersionManifest types + signature verification
  pkg/update/updater_test.go                   Self-update tests (mock manifest server)
  internal/config/enterprise.go                Enterprise config extensions
  cmd/relay/main.go                            Wires RedisRegistry, VaultStore, SIEMEmitter, fd-passing
  cmd/agent/main.go                            Wires VaultStore, self-updater
  Makefile                                     Build targets mirroring community
  .golangci.yml                                Linter config (copy of community)
  .github/workflows/ci.yml                     CI: build + test enterprise, build community
  CLAUDE.md                                    Enterprise repo conventions
  LICENSE                                      Commercial license
```

### Interface Wiring Pattern

Community `cmd/relay/main.go` (current):
```
store    := auth.NewFileStore()
registry := relay.NewMemoryRegistry(logger)
emitter  := audit.NewSlogEmitter(logger, audit.DefaultBufferSize)
```

Enterprise `cmd/relay/main.go` (new):
```
store    := vault.NewVaultStore(vaultClient, logger)     // satisfies auth.CertificateStore
registry := redis.NewRedisRegistry(redisClient, logger)  // satisfies relay.AgentRegistry
emitter  := siem.NewSIEMEmitter(kafkaProducer, logger)   // satisfies audit.Emitter
```

Everything downstream of the wiring point (agent listener, port router, client listener, admin server) is unchanged. They accept interfaces, not concrete types.

---

## Package Layout: atlax-enterprise/

```
atlax-enterprise/
  go.mod
  go.sum
  Makefile
  .golangci.yml
  .github/
    workflows/
      ci.yml
  LICENSE
  README.md
  cmd/
    relay/
      main.go                         Enterprise relay entry point
    agent/
      main.go                         Enterprise agent entry point
  pkg/
    redis/
      registry.go                     RedisRegistry implementation
      registry_test.go                Tests with miniredis
    vault/
      certstore.go                    VaultStore implementation
      certstore_test.go               Tests with mock Vault client
    siem/
      emitter.go                      SIEMEmitter (Kafka/NATS)
      emitter_test.go                 Tests with in-memory broker
    graceful/
      restart.go                      Fd-passing binary swap (relay)
      restart_test.go                 Tests for fd inheritance and drain
      fdenv.go                        LISTEN_FDS environment helpers
    update/
      updater.go                      Agent self-update orchestrator
      manifest.go                     VersionManifest types + ed25519 verification
      atomic.go                       Atomic binary replacement
      rollback.go                     Crash rollback logic
      updater_test.go                 Tests with mock manifest server
      manifest_test.go                Signature verification tests
  internal/
    config/
      enterprise.go                   Enterprise-specific config extensions
      enterprise_test.go              Config loading tests
  configs/
    relay-enterprise.example.yaml     Enterprise relay config example
    agent-enterprise.example.yaml     Enterprise agent config example
  docs/
    architecture.md                   Enterprise architecture overview
    deployment.md                     Deployment guide
```

---

## Dependency Graph

```
Step 6 (Enterprise repo setup + community interface contract)
   |
   +---> Step 7a (Zero-downtime relay binary swap)
   |        |
   +---> Step 7b (Agent self-update)
   |        |
   |        v
   |     [Steps 7a and 7b are independent of each other]
   |
   +---> [Step 7a + 7b must complete before Step 8]
            |
            v
         Step 8a (RedisRegistry)
            |
         Step 8b (VaultStore)  [independent of 8a]
            |
         Step 8c (SIEMEmitter) [independent of 8a, 8b]
            |
            v
         [Steps 8a, 8b, 8c are independent of each other
          but all depend on Step 6 for repo structure]
```

Steps 7a and 7b can be developed in parallel. Steps 8a, 8b, and 8c can be developed in parallel. Step 6 must complete first as it establishes the repo structure, build system, and CI that all subsequent steps depend on.

---

## Invariants (verified after EVERY step)

1. Community: `cd atlax && go build ./...` passes
2. Community: `cd atlax && go test -race ./...` passes
3. Community: `cd atlax && golangci-lint run ./...` passes
4. Enterprise: `cd atlax-enterprise && go build ./...` passes
5. Enterprise: `cd atlax-enterprise && go test -race ./...` passes
6. Enterprise: `cd atlax-enterprise && golangci-lint run ./...` passes
7. Enterprise binary is a drop-in replacement for community (same config format, same protocol)
8. No community code imports from the enterprise module
9. All enterprise types include compile-time interface checks (`var _ Interface = (*Impl)(nil)`)
10. Coverage for changed enterprise packages >= 80%
11. No function > 50 lines, no file > 800 lines
12. Step report written immediately after each step

---

## Step 6: Enterprise Repo Setup + Community Interface Contract

**Branch:** `phase6/enterprise-init` (on `atlax-enterprise` repo) + `phase6/interface-contract` (on `atlax` repo)
**Depends on:** Step 5 (v0.1.0 tag on community)
**Closes:** N/A (infrastructure step)

### Context Brief

This step creates the `atlax-enterprise` repository from scratch and documents the interface stability contract in the community repo. The enterprise repo must be a fully buildable Go module that imports the community module at `v0.1.0`, compiles both relay and agent binaries, passes lint, and has CI that catches community interface breaks. The community repo gets a new `docs/api/interfaces.md` file that formally documents which interfaces are the enterprise API surface and the rules for changing them.

The enterprise `main.go` files initially wire the same community implementations (MemoryRegistry, FileStore, SlogEmitter) as placeholders. Subsequent steps replace these with enterprise implementations one at a time. This ensures the repo is buildable and testable from the first commit.

### Tasks

#### 6.1: Community interface contract document

- [ ] Create `docs/api/interfaces.md` in the `atlax` community repo
- [ ] Document the following interfaces as the enterprise API surface:
  - `pkg/relay.AgentRegistry` (5 methods: Register, Unregister, Lookup, Heartbeat, ListConnectedAgents)
  - `pkg/relay.AgentConnection` (6 methods: CustomerID, Muxer, RemoteAddr, ConnectedAt, LastSeen, Close)
  - `pkg/relay.TrafficRouter` (3 methods: Route, AddPortMapping, RemovePortMapping)
  - `pkg/relay.Server` (3 methods: Start, Stop, Addr)
  - `pkg/auth.CertificateStore` (3 methods: LoadCertificate, LoadCertificateAuthority, WatchForRotation)
  - `pkg/auth.TLSConfigurator` (2 methods: ServerTLSConfig, ClientTLSConfig)
  - `internal/audit.Emitter` (2 methods: Emit, Close)
- [ ] Document stability rules:
  - Adding methods to an existing interface is a breaking change
  - New extension points use new interfaces, not method additions
  - Enterprise CI builds against the community module; any break is caught immediately
  - Breaking changes require a coordinated community + enterprise version bump
- [ ] Document exported types that are part of the contract:
  - `relay.AgentInfo`, `relay.PortAllocation`, `relay.TrafficRouterConfig`, `relay.ServerConfig`
  - `auth.CertRotationConfig`, `auth.CertInfo`, `auth.Identity`, `auth.TLSPaths`, `auth.TLSOption`
  - `audit.Event`, `audit.Action` (all constants)
  - `config.RelayConfig`, `config.AgentConfig`, `config.UpdateConfig` and all nested config types
- [ ] Note that `internal/audit` is importable by enterprise because Go module boundaries allow it (enterprise is a separate module, not a package within the community module). Clarify this is intentional.

#### 6.2: Enterprise go.mod

- [ ] Initialize `atlax-enterprise/go.mod` with:
  - Module path: `github.com/atlasshare/atlax-enterprise`
  - Go version: `go 1.25.8` (match community)
  - Direct require: `github.com/atlasshare/atlax v0.1.0`
  - Direct require: `gopkg.in/yaml.v3 v3.0.1` (config loading)
  - Direct require: `github.com/stretchr/testify v1.11.1` (testing)
  - Enterprise-specific deps added in later steps (redis, vault, kafka -- NOT in Step 6)
- [ ] Run `go mod tidy` to populate `go.sum`
- [ ] Verify `go build ./...` succeeds

#### 6.3: Enterprise cmd/relay/main.go (placeholder wiring)

- [ ] Create `cmd/relay/main.go` in the enterprise repo
- [ ] Structure mirrors community `cmd/relay/main.go` exactly (same `run()` pattern, same signal handling, same graceful shutdown)
- [ ] Import community packages:
  - `github.com/atlasshare/atlax/internal/audit`
  - `github.com/atlasshare/atlax/internal/config`
  - `github.com/atlasshare/atlax/pkg/auth`
  - `github.com/atlasshare/atlax/pkg/relay`
  - `github.com/prometheus/client_golang/prometheus`
- [ ] Initially wire community implementations as placeholders:
  - `store := auth.NewFileStore()` (placeholder until Step 8b replaces with VaultStore)
  - `registry := relay.NewMemoryRegistry(logger)` (placeholder until Step 8a replaces with RedisRegistry)
  - `emitter := audit.NewSlogEmitter(logger, audit.DefaultBufferSize)` (placeholder until Step 8c replaces with SIEMEmitter)
- [ ] Add SIGUSR2 signal handling stub (no-op initially, wired in Step 7a)
- [ ] Add comment markers at each wiring point: `// ENTERPRISE: replace with RedisRegistry in Step 8a`
- [ ] `initLogger` function copied from community (identical)

#### 6.4: Enterprise cmd/agent/main.go (placeholder wiring)

- [ ] Create `cmd/agent/main.go` in the enterprise repo
- [ ] Structure mirrors community `cmd/agent/main.go` exactly
- [ ] Import community packages:
  - `github.com/atlasshare/atlax/internal/audit`
  - `github.com/atlasshare/atlax/internal/config`
  - `github.com/atlasshare/atlax/pkg/agent`
  - `github.com/atlasshare/atlax/pkg/auth`
- [ ] Initially wire community implementations as placeholders:
  - `store := auth.NewFileStore()` (placeholder until Step 8b)
  - `emitter := audit.NewSlogEmitter(logger, audit.DefaultBufferSize)` (placeholder until Step 8c)
- [ ] Add update check stub (no-op initially, wired in Step 7b)
- [ ] `initLogger` function copied from community (identical)

#### 6.5: Enterprise Makefile

- [ ] Create `Makefile` mirroring community build targets
- [ ] Version info via ldflags (same pattern as community, but using `github.com/atlasshare/atlax-enterprise/internal/config` if enterprise has its own version, or the community `internal/config` path)
- [ ] Targets:
  - `build`: Build `bin/atlax-relay-enterprise` and `bin/atlax-agent-enterprise`
  - `build-relay`: Build relay binary only
  - `build-agent`: Build agent binary only
  - `test`: Run `go test -race -coverprofile=coverage.out -covermode=atomic ./...`
  - `lint`: Run `golangci-lint run --config .golangci.yml ./...`
  - `fmt`: Run `gofmt -w .` and `goimports -w .`
  - `vet`: Run `go vet ./...`
  - `clean`: Remove `bin/`, `coverage.out`, `coverage.html`
  - `coverage`: Generate HTML coverage report
  - `help`: Show target descriptions
- [ ] Binary names are `atlax-relay-enterprise` and `atlax-agent-enterprise` (distinct from community)

#### 6.6: Enterprise .golangci.yml

- [ ] Copy community `.golangci.yml` verbatim
- [ ] Update `goimports` local-prefixes to include both modules:
  - `github.com/atlasshare/atlax-enterprise`
  - `github.com/atlasshare/atlax`

#### 6.7: Enterprise CLAUDE.md

No CLAUDE.md in this repo. Enterprise conventions live in the workspace-level `atlax-department/CLAUDE.md` under the "Enterprise Repo Conventions" section. This avoids duplication and keeps all conventions in one place.

#### 6.8: Enterprise LICENSE

- [ ] Create `LICENSE` file with commercial license header
- [ ] Include: proprietary notice, no redistribution without license, copyright AtlasShare
- [ ] NOT Apache 2.0 (that is community only)

#### 6.9: Enterprise GitHub Actions CI

- [ ] Create `.github/workflows/ci.yml` with:
  - Trigger: push to `main`, pull requests to `main`
  - Go version: `1.25.x`
  - `GOPRIVATE=github.com/atlasshare/*` environment variable
  - Steps:
    1. Checkout enterprise repo
    2. Checkout community repo (for `go.work` or to ensure module resolution)
    3. Setup Go 1.25.x
    4. Configure git for private module access: `git config --global url."https://${GITHUB_TOKEN}@github.com/".insteadOf "https://github.com/"`
    5. `go mod download`
    6. `make lint`
    7. `make test`
    8. `make build`
  - Secret: `GITHUB_TOKEN` with read access to `atlax` community repo
  - Artifact: upload `bin/atlax-relay-enterprise` and `bin/atlax-agent-enterprise`
- [ ] The CI implicitly validates that the community interfaces have not broken: if a community interface changes and the enterprise type no longer satisfies it, `go build` fails

#### 6.10: go.work for local development

- [ ] Create `go.work` in the `atlax-department/` parent directory (NOT checked into either repo)
- [ ] Contents:
  ```
  go 1.25.8

  use (
      ./atlax
      ./atlax-enterprise
  )
  ```
- [ ] Add `go.work` and `go.work.sum` to `.gitignore` in both repos
- [ ] Document in enterprise CLAUDE.md: "For local development, create `go.work` in the parent directory. This allows enterprise code to reference local community changes without publishing a new tag. The `go.work` file is NOT committed to either repository."
- [ ] Verify: with `go.work` active, `cd atlax-enterprise && go build ./...` uses the local `atlax/` source, not the tagged v0.1.0

#### 6.11: Enterprise README.md

- [ ] Create `README.md` with:
  - One-paragraph description of atlax-enterprise
  - Prerequisites: Go 1.25+, access to community module, `GOPRIVATE` configuration
  - Build instructions
  - Relationship to community edition
  - Link to interface contract in community repo

### Exit Criteria

- [ ] `cd atlax-enterprise && make build` produces `bin/atlax-relay-enterprise` and `bin/atlax-agent-enterprise`
- [ ] `cd atlax-enterprise && make test` passes (placeholder tests or no tests yet is acceptable for Step 6)
- [ ] `cd atlax-enterprise && make lint` passes
- [ ] Enterprise `go.mod` requires `github.com/atlasshare/atlax v0.1.0`
- [ ] `go.work` in parent directory allows local development
- [ ] `docs/api/interfaces.md` exists in community repo
- [ ] Enterprise CI workflow exists and would pass (verified locally)
- [ ] Enterprise binaries produce identical behavior to community binaries (same config, same protocol)
- [ ] Step report written

---

## Step 7a: Zero-Downtime Relay Binary Swap

**Branch:** `phase6/fd-passing` (on `atlax-enterprise` repo)
**Depends on:** Step 6 (enterprise repo exists with buildable relay)
**Closes:** #61

### Context Brief

The community relay requires a full restart for binary updates. During restart, all agent connections drop and must reconnect (5-10 second outage). The enterprise relay uses SIGUSR2-triggered fd passing to hand off listening sockets to a new process, drain active connections in the old process, and achieve zero-downtime binary swaps.

The implementation lives entirely in `pkg/graceful/` in the enterprise repo. The enterprise `cmd/relay/main.go` registers the SIGUSR2 handler and calls into the graceful package. The community relay is unaffected.

The mechanism:
1. Operator sends SIGUSR2 to the running relay process.
2. The relay forks/execs the new binary, passing listening file descriptors via `LISTEN_FDS` environment variable (systemd socket activation protocol).
3. The new process detects `LISTEN_FDS`, recovers the listeners from the inherited fds, and begins accepting new connections.
4. The new process signals readiness (writes to a pipe inherited from the old process).
5. The old process stops accepting new connections, sends GOAWAY to all agents, and waits for active streams to drain (up to `shutdown_grace_period`).
6. The old process exits.

### Tasks

#### 7a.1: pkg/graceful/fdenv.go -- fd environment helpers

- [ ] `SetListenFDs(fds []*os.File) []string` -- builds the environment variables for the child process: `LISTEN_FDS=N`, `LISTEN_PID=<child-pid>`, and `LISTEN_FDNAMES=agent,client,admin` (one name per fd)
- [ ] `GetListenFDs() ([]*os.File, error)` -- reads `LISTEN_FDS` and `LISTEN_PID` from environment, recovers the file descriptors starting at fd 3 (systemd convention), validates PID matches current process
- [ ] `IsInherited() bool` -- returns true if `LISTEN_FDS` is set and `LISTEN_PID` matches
- [ ] Tests: set env vars, verify fd count and names; verify PID mismatch returns empty

#### 7a.2: pkg/graceful/restart.go -- restart orchestrator

- [ ] `type Restarter struct` with fields: `listeners []net.Listener`, `binaryPath string`, `readyPipe *os.File`, `logger *slog.Logger`
- [ ] `func NewRestarter(listeners []net.Listener, binaryPath string, logger *slog.Logger) *Restarter`
- [ ] `func (r *Restarter) Restart(ctx context.Context) error`:
  1. Create a pipe for readiness signaling (parent reads, child writes)
  2. Extract file descriptors from each listener via `listener.(interface{ File() (*os.File, error) }).File()`
  3. Build `os.ProcAttr` with the current environment plus `LISTEN_FDS` variables, plus the readiness pipe fd
  4. Start the new process via `os.StartProcess(r.binaryPath, os.Args, attr)`
  5. Wait for readiness signal on the pipe (with timeout from context)
  6. Return nil on success (caller is responsible for drain + exit)
- [ ] `func (r *Restarter) DrainAndExit(ctx context.Context, server relay.Server) error`:
  1. Call `server.Stop(ctx)` which sends GOAWAY and drains
  2. Log completion
  3. Call `os.Exit(0)`
- [ ] Tests:
  - Test fd extraction from `net.TCPListener`
  - Test readiness pipe communication (parent/child simulation)
  - Test timeout when child never signals readiness

#### 7a.3: Enterprise cmd/relay/main.go -- SIGUSR2 handler

- [ ] Register SIGUSR2 in the signal notification alongside SIGINT/SIGTERM
- [ ] On SIGUSR2:
  1. Collect all active listeners (agent listener, client listeners for each port, admin listener)
  2. Create `Restarter` with the listeners and current binary path (`os.Executable()`)
  3. Call `restarter.Restart(ctx)` to fork/exec new process
  4. On success: call `restarter.DrainAndExit(ctx, server)` to drain and exit
  5. On failure: log error, continue running (do not exit on failed restart)
- [ ] Recovery from inherited fds on startup:
  1. Before `net.Listen`, check `graceful.IsInherited()`
  2. If inherited: call `graceful.GetListenFDs()` and wrap each fd as a `net.Listener` via `net.FileListener(fd)`
  3. Pass recovered listeners to `AgentListener`, `ClientListener`, `AdminServer` instead of having them call `net.Listen`
  4. If not inherited: proceed with normal `net.Listen` (community behavior)
- [ ] Readiness signaling: after all listeners are active (inherited or fresh), write to the readiness pipe fd (env var `READY_PIPE_FD`) if present

#### 7a.4: Integration test under load

- [ ] Test: start enterprise relay with echo agent, open 100 concurrent streams, send SIGUSR2, verify:
  - New process starts and accepts new connections
  - Old process drains existing streams (no data loss)
  - Old process exits after drain
  - Zero connection errors observed by clients
- [ ] Test: SIGUSR2 with non-existent new binary (binary deleted) -- verify old process continues running
- [ ] Test: SIGUSR2 when new process fails to start (bad binary) -- verify old process continues running

### Exit Criteria

- [ ] `kill -USR2 <relay-pid>` triggers zero-downtime binary swap
- [ ] Existing connections drain gracefully (GOAWAY + stream completion)
- [ ] New connections are accepted by the new process immediately
- [ ] Failed restart does not crash the old process
- [ ] `var _ relay.Server = (*Restarter)(nil)` is NOT needed (Restarter is not a Server; it wraps one)
- [ ] Coverage for `pkg/graceful/` >= 80%
- [ ] Step report written

---

## Step 7b: Agent Self-Update

**Branch:** `phase6/self-update` (on `atlax-enterprise` repo)
**Depends on:** Step 6 (enterprise repo exists with buildable agent)
**Closes:** #62

### Context Brief

The community agent requires manual binary replacement and process restart for updates. The enterprise agent periodically polls a manifest URL, verifies the manifest's ed25519 signature, downloads the new binary, verifies its SHA-256 hash and file size, performs an atomic binary replacement, and restarts. If the new binary crashes within a configurable rollback window (default 60 seconds), the previous binary is restored automatically.

The implementation lives in `pkg/update/` in the enterprise repo. The enterprise `cmd/agent/main.go` starts the update checker as a background goroutine. The community agent is unaffected (its `config.UpdateConfig.Enabled` defaults to `false`).

### Tasks

#### 7b.1: pkg/update/manifest.go -- manifest types and verification

- [ ] `type VersionManifest struct` with fields matching `docs/api/update-manifest.md`:
  - `Version string`, `ReleaseDate string`, `MinAgentVersion string`, `ChangelogURL string`
  - `Binaries map[string]BinaryInfo`, `Signature string`
- [ ] `type BinaryInfo struct` with `URL string`, `SHA256 string`, `Size int64`
- [ ] `func VerifyManifest(manifest []byte, publicKey ed25519.PublicKey) (*VersionManifest, error)`:
  1. Parse JSON into `VersionManifest`
  2. Extract `Signature` field
  3. Set `Signature` to empty string in a copy of the manifest JSON
  4. Verify ed25519 signature of the modified JSON against the public key
  5. Return parsed manifest on success, error on verification failure
- [ ] `func CompareVersions(current, available string) (int, error)` -- semver comparison: returns -1 (current older), 0 (same), 1 (current newer)
- [ ] Tests:
  - Generate ed25519 key pair in test
  - Sign a test manifest, verify it passes verification
  - Tamper with manifest, verify it fails verification
  - Tamper with signature, verify it fails
  - Version comparison: "1.0.0" < "1.1.0", "1.1.0" == "1.1.0", "2.0.0" > "1.9.9"

#### 7b.2: pkg/update/atomic.go -- atomic binary replacement

- [ ] `func AtomicReplace(currentPath, newPath string) (backupPath string, err error)`:
  1. `backupPath = currentPath + ".old"`
  2. `os.Rename(currentPath, backupPath)` -- back up current
  3. `os.Rename(newPath, currentPath)` -- atomic swap
  4. On rename failure: attempt `os.Rename(backupPath, currentPath)` to restore backup
  5. Return backupPath for rollback tracking
- [ ] `func SetExecutable(path string) error` -- `os.Chmod(path, 0755)`
- [ ] Tests:
  - Create temp files, call AtomicReplace, verify new file is at current path, old is at backup path
  - Verify executable permission is set
  - Test failure recovery: make currentPath read-only, verify backup is restored

#### 7b.3: pkg/update/rollback.go -- crash rollback

- [ ] `const DefaultRollbackWindow = 60 * time.Second`
- [ ] `func RecordUpdate(timestampFile string) error` -- write current unix timestamp to file
- [ ] `func ShouldRollback(timestampFile, backupPath string, window time.Duration) bool`:
  1. Check if `backupPath` exists
  2. Check if `timestampFile` exists
  3. Read timestamp, compute elapsed time
  4. Return true if elapsed < window (crash happened too soon after update)
- [ ] `func Rollback(currentPath, backupPath, timestampFile string) error`:
  1. `os.Rename(backupPath, currentPath)` -- restore old binary
  2. `os.Remove(timestampFile)` -- clear update record
- [ ] `func ClearBackup(backupPath, timestampFile string)` -- called when agent has been running longer than rollback window (successful update)
- [ ] Tests:
  - Write timestamp, check ShouldRollback within window -- returns true
  - Write timestamp, advance past window, check ShouldRollback -- returns false
  - No backup file -- ShouldRollback returns false
  - Rollback restores old binary

#### 7b.4: pkg/update/updater.go -- update orchestrator

- [ ] `type Updater struct` with fields: `config config.UpdateConfig`, `publicKey ed25519.PublicKey`, `currentVersion string`, `binaryPath string`, `httpClient *http.Client`, `logger *slog.Logger`
- [ ] `func NewUpdater(cfg config.UpdateConfig, publicKey ed25519.PublicKey, currentVersion string, logger *slog.Logger) (*Updater, error)`:
  1. Validate config (ManifestURL must be HTTPS, CheckInterval > 0)
  2. Resolve current binary path via `os.Executable()` + `filepath.EvalSymlinks()`
  3. Return configured Updater
- [ ] `func (u *Updater) Start(ctx context.Context) error`:
  1. On startup: check `ShouldRollback` -- if true, execute rollback and return error
  2. Start ticker at `config.CheckInterval`
  3. On each tick: call `u.check(ctx)`
  4. Block until `ctx.Done()`
- [ ] `func (u *Updater) check(ctx context.Context) error`:
  1. Fetch manifest from `config.ManifestURL` via HTTPS GET
  2. Verify manifest signature via `VerifyManifest`
  3. Compare versions -- if not newer, return nil
  4. Select binary for `runtime.GOOS + "-" + runtime.GOARCH`
  5. Download binary to temp file in same directory as current binary
  6. Verify SHA-256 hash matches `BinaryInfo.SHA256`
  7. Verify file size matches `BinaryInfo.Size`
  8. Set executable permission
  9. Call `AtomicReplace`
  10. Call `RecordUpdate`
  11. Log the update
  12. Restart: `syscall.Exec(u.binaryPath, os.Args, os.Environ())`
- [ ] Tests (with mock HTTP server):
  - Mock manifest server returns valid signed manifest with newer version
  - Updater downloads, verifies, and replaces binary (verify file content at binary path matches mock binary)
  - Mock server returns same version -- no update performed
  - Mock server returns invalid signature -- update aborted
  - Mock server returns wrong SHA-256 -- download rejected
  - Mock server returns HTTP URL -- rejected (HTTPS only)
  - Check interval respected (no premature polls)

#### 7b.5: Enterprise cmd/agent/main.go -- update wiring

- [ ] Load ed25519 public key:
  - Compile-time embed via `//go:embed update_pubkey.pem` OR
  - Load from `config.UpdateConfig.PublicKeyPath` if set
  - If neither available and `UpdateConfig.Enabled` is true, return startup error
- [ ] If `config.UpdateConfig.Enabled`:
  1. Create `Updater` with config, public key, and current version (from ldflags)
  2. Start updater in background goroutine: `go updater.Start(ctx)`
  3. On context cancellation (shutdown), updater stops
- [ ] If `config.UpdateConfig.Enabled` is false: skip (community behavior)

#### 7b.6: Rollback guard script

- [ ] Create `scripts/atlax-update-guard.sh` in enterprise repo
- [ ] Script checks for `.old` backup and recent update timestamp
- [ ] If both conditions met (crash within rollback window): restore old binary, log rollback
- [ ] Intended as `ExecStartPre` in systemd unit
- [ ] Create `deployments/systemd/atlax-agent-enterprise.service` that includes the guard

### Exit Criteria

- [ ] Agent checks for updates at configured interval
- [ ] Ed25519 signature verification prevents tampered manifests
- [ ] SHA-256 + size verification prevents corrupted downloads
- [ ] Binary replacement is atomic (no window with missing binary)
- [ ] Crash within rollback window triggers automatic rollback to previous version
- [ ] HTTPS-only download (HTTP URLs rejected)
- [ ] `config.UpdateConfig.Enabled = false` disables all update behavior
- [ ] Coverage for `pkg/update/` >= 80%
- [ ] Step report written

---

## Step 8a: RedisRegistry

**Branch:** `phase6/redis-registry` (on `atlax-enterprise` repo)
**Depends on:** Step 6 (repo structure), Steps 7a+7b complete
**Closes:** #63 (multi-agent support)

### Context Brief

The community `MemoryRegistry` stores agent connections in a `sync.RWMutex`-protected `map[string]*LiveConnection`. It supports one connection per customer and provides no cross-relay lookup. The enterprise `RedisRegistry` stores agent metadata in Redis hashes with TTL-based expiry, supports multiple connections per customer with round-robin routing, and enables cross-relay agent lookup for active-active deployments.

The `RedisRegistry` satisfies `relay.AgentRegistry`. It stores agent metadata (customer ID, relay ID, remote addr, connected at, cert serial, assigned ports) in a Redis hash keyed by `atlax:agent:{customerID}:{connectionID}`. A secondary index `atlax:customer:{customerID}` is a Redis set of connection IDs for round-robin selection. TTL is 90 seconds, refreshed on every heartbeat.

Local connections (agents connected to THIS relay instance) are also tracked in memory for direct mux access. Redis stores the metadata for cross-relay discovery.

### Tasks

#### 8a.1: Redis client dependency

- [ ] Add `github.com/redis/go-redis/v9` to enterprise `go.mod`
- [ ] Add `github.com/alicebob/miniredis/v2` to enterprise `go.mod` (test dependency)
- [ ] Run `go mod tidy`

#### 8a.2: pkg/redis/registry.go -- RedisRegistry implementation

- [ ] `type RedisRegistry struct` with fields:
  - `client *redis.Client` -- Redis client
  - `relayID string` -- identifier for this relay instance (from config or hostname)
  - `ttl time.Duration` -- TTL for Redis keys (default 90s)
  - `local sync.RWMutex` -- protects localConns
  - `localConns map[string][]relay.AgentConnection` -- local connections for direct mux access
  - `logger *slog.Logger`
- [ ] Compile-time interface check: `var _ relay.AgentRegistry = (*RedisRegistry)(nil)`
- [ ] `func NewRedisRegistry(client *redis.Client, relayID string, ttl time.Duration, logger *slog.Logger) *RedisRegistry`
- [ ] `func (r *RedisRegistry) Register(ctx context.Context, customerID string, conn relay.AgentConnection) error`:
  1. Generate connection ID: `{relayID}:{remoteAddr}`
  2. Store in Redis hash `atlax:agent:{customerID}:{connID}` with fields: relay_id, remote_addr, connected_at, last_seen
  3. Set TTL on the hash key
  4. Add connID to Redis set `atlax:customer:{customerID}`
  5. Store in `localConns[customerID]` (append to slice)
  6. If slice length exceeds customer max_connections: remove oldest, send GOAWAY, close
- [ ] `func (r *RedisRegistry) Unregister(ctx context.Context, customerID string) error`:
  1. Remove all entries for customerID from Redis (all connection hashes + customer set)
  2. Remove from `localConns`
  3. Close all local connections for this customer
- [ ] `func (r *RedisRegistry) Lookup(ctx context.Context, customerID string) (relay.AgentConnection, error)`:
  1. Check `localConns[customerID]` first -- if local connections exist, select via round-robin (rotate index)
  2. If no local connections: query Redis set `atlax:customer:{customerID}` to check if agent is on another relay
  3. If on another relay: return `relay.ErrAgentNotFound` with metadata indicating which relay has the agent (cross-relay forwarding is a separate future concern)
  4. If nowhere: return `relay.ErrAgentNotFound`
- [ ] `func (r *RedisRegistry) Heartbeat(ctx context.Context, customerID string) error`:
  1. Update `last_seen` field in all Redis hashes for this customer
  2. Reset TTL on all hash keys
  3. Update `LastSeen` on local connections
- [ ] `func (r *RedisRegistry) ListConnectedAgents(ctx context.Context) ([]relay.AgentInfo, error)`:
  1. Scan Redis keys matching `atlax:agent:*`
  2. Build `AgentInfo` slice from hash fields
  3. Enrich with local stream counts where available

#### 8a.3: pkg/redis/registry_test.go -- tests with miniredis

- [ ] Setup helper: start `miniredis.Run()`, create `redis.NewClient` pointing to miniredis addr
- [ ] Test: Register + Lookup returns the registered connection
- [ ] Test: Register two connections for same customer (multi-agent), Lookup returns them in round-robin order
- [ ] Test: Register exceeds max_connections, oldest connection gets GOAWAY and is closed
- [ ] Test: Unregister removes all Redis keys and closes local connections
- [ ] Test: Heartbeat refreshes TTL (check with miniredis TTL inspection)
- [ ] Test: TTL expiry removes agent (advance miniredis clock past TTL, verify Lookup returns ErrAgentNotFound)
- [ ] Test: ListConnectedAgents returns all agents across all customers
- [ ] Test: Lookup for agent on different relay returns ErrAgentNotFound (cross-relay forwarding not implemented yet)
- [ ] Test: concurrent Register/Unregister/Lookup with goroutines (race detector validation)

#### 8a.4: Enterprise cmd/relay/main.go -- wire RedisRegistry

- [ ] Add Redis connection config to enterprise config extensions:
  - `redis.addr`, `redis.password`, `redis.db`, `redis.tls_enabled`
- [ ] Replace `relay.NewMemoryRegistry(logger)` placeholder with:
  1. Create `redis.NewClient` from config
  2. Ping Redis to verify connectivity
  3. Create `redis.NewRedisRegistry(client, relayID, 90*time.Second, logger)`
- [ ] Fallback: if Redis config is empty, fall back to `relay.NewMemoryRegistry(logger)` with a log warning (allows enterprise binary to run without Redis for testing)

#### 8a.5: Enterprise config extension

- [ ] Create `internal/config/enterprise.go` with:
  - `type EnterpriseRelayConfig struct` embedding `config.RelayConfig` and adding:
    - `Redis RedisConfig`
    - `Vault VaultConfig` (placeholder for Step 8b)
    - `SIEM SIEMConfig` (placeholder for Step 8c)
  - `type RedisConfig struct`: `Addr string`, `Password string`, `DB int`, `TLSEnabled bool`, `PoolSize int`
  - `type VaultConfig struct`: placeholder fields
  - `type SIEMConfig struct`: placeholder fields
- [ ] Enterprise config loader that embeds community loader and adds Redis/Vault/SIEM sections
- [ ] Tests: load enterprise config YAML, verify Redis fields parsed correctly

### Exit Criteria

- [ ] `var _ relay.AgentRegistry = (*RedisRegistry)(nil)` compiles
- [ ] Multiple connections per customer with round-robin selection
- [ ] Redis TTL expiry cleans up dead agents automatically
- [ ] Heartbeat refreshes TTL
- [ ] Enterprise relay binary uses RedisRegistry when Redis config is present
- [ ] Enterprise relay binary falls back to MemoryRegistry when Redis config is absent
- [ ] All tests pass with miniredis (no real Redis required for CI)
- [ ] Coverage for `pkg/redis/` >= 80%
- [ ] Step report written

---

## Step 8b: VaultStore

**Branch:** `phase6/vault-store` (on `atlax-enterprise` repo)
**Depends on:** Step 6 (repo structure), Steps 7a+7b complete
**Closes:** #69 (certificate issuance automation)

### Context Brief

The community `FileStore` loads PEM certificates from disk and polls for file changes. The enterprise `VaultStore` integrates with HashiCorp Vault PKI secrets engine (or step-ca) for automated certificate issuance, renewal, and rotation. Instead of manually generating and distributing certificates, the VaultStore generates CSRs, submits them to Vault, retrieves signed certificates, and handles automatic renewal before expiry.

The `VaultStore` satisfies `auth.CertificateStore`. It implements the same three methods: `LoadCertificate`, `LoadCertificateAuthority`, and `WatchForRotation`. The difference is that `LoadCertificate` can issue a new certificate from Vault if the local file is missing or expired, and `WatchForRotation` monitors both file changes AND certificate expiry to trigger Vault-based renewal.

### Tasks

#### 8b.1: Vault client dependency

- [ ] Add `github.com/hashicorp/vault/api` to enterprise `go.mod`
- [ ] Run `go mod tidy`

#### 8b.2: pkg/vault/certstore.go -- VaultStore implementation

- [ ] `type VaultStore struct` with fields:
  - `vaultClient *api.Client` -- Vault API client
  - `pkiMount string` -- Vault PKI mount path (default `pki`)
  - `role string` -- Vault PKI role name for certificate issuance
  - `fileStore *auth.FileStore` -- embedded community FileStore for file-based operations
  - `renewBefore time.Duration` -- renew certificate this long before expiry (default 7 days)
  - `logger *slog.Logger`
- [ ] Compile-time interface check: `var _ auth.CertificateStore = (*VaultStore)(nil)`
- [ ] `func NewVaultStore(vaultClient *api.Client, pkiMount, role string, renewBefore time.Duration, logger *slog.Logger) *VaultStore`
- [ ] `func (v *VaultStore) LoadCertificate(certPath, keyPath string) (tls.Certificate, error)`:
  1. Try loading from disk via embedded `fileStore.LoadCertificate`
  2. If file exists and not expired: return it
  3. If file missing or expired or expiring within `renewBefore`:
     a. Generate a new private key (ECDSA P-256)
     b. Build CSR with the appropriate CN (extracted from existing cert if renewing, or from config)
     c. Submit CSR to Vault PKI: `POST /v1/{pkiMount}/sign/{role}`
     d. Parse the response: signed certificate + CA chain
     e. Write new cert to `certPath`, new key to `keyPath` (0600 permissions)
     f. Return the new `tls.Certificate`
  4. Log the issuance/renewal event
- [ ] `func (v *VaultStore) LoadCertificateAuthority(path string) (*x509.CertPool, error)`:
  1. Try loading from disk via embedded `fileStore.LoadCertificateAuthority`
  2. If file missing: fetch CA cert from Vault: `GET /v1/{pkiMount}/ca/pem`
  3. Write to disk at `path`
  4. Return the pool
- [ ] `func (v *VaultStore) WatchForRotation(ctx context.Context, certPath, keyPath string, reload func(tls.Certificate)) error`:
  1. Start the file-based watcher (community behavior) in a goroutine
  2. Also start a certificate expiry watcher:
     a. Load current cert, parse expiry
     b. Sleep until `expiry - renewBefore`
     c. Call `v.LoadCertificate(certPath, keyPath)` which triggers Vault renewal
     d. Call `reload` with the new certificate
     e. Loop
  3. Block until `ctx.Done()`

#### 8b.3: pkg/vault/certstore_test.go -- tests with mock Vault

- [ ] Mock Vault HTTP server using `httptest.NewServer`:
  - `POST /v1/pki/sign/agent-role` -- returns signed certificate JSON
  - `GET /v1/pki/ca/pem` -- returns CA PEM
- [ ] Test: LoadCertificate with missing file triggers Vault issuance, cert written to disk
- [ ] Test: LoadCertificate with valid non-expired file returns file cert (no Vault call)
- [ ] Test: LoadCertificate with expired file triggers Vault renewal
- [ ] Test: LoadCertificate with cert expiring within `renewBefore` triggers renewal
- [ ] Test: LoadCertificateAuthority with missing file fetches from Vault
- [ ] Test: WatchForRotation triggers renewal before expiry (use short-lived test cert)
- [ ] Test: Vault unavailable -- return error (not silent failure)
- [ ] Test: Vault returns invalid certificate -- return error

#### 8b.4: Enterprise wiring

- [ ] Add Vault config to `VaultConfig` struct:
  - `Addr string`, `Token string`, `TokenPath string`, `PKIMount string`, `Role string`
  - `RenewBefore time.Duration`, `TLSSkipVerify bool` (testing only)
- [ ] In enterprise `cmd/relay/main.go`: replace `auth.NewFileStore()` with:
  1. If Vault config is present: create `api.NewClient`, create `vault.NewVaultStore`
  2. If Vault config is absent: fall back to `auth.NewFileStore()`
- [ ] In enterprise `cmd/agent/main.go`: same wiring pattern

### Exit Criteria

- [ ] `var _ auth.CertificateStore = (*VaultStore)(nil)` compiles
- [ ] Certificates auto-issued from Vault when files are missing
- [ ] Certificates auto-renewed before expiry via Vault
- [ ] File-based fallback works when cert files exist and are valid
- [ ] Enterprise binary falls back to FileStore when Vault config is absent
- [ ] All tests pass with mock Vault (no real Vault required for CI)
- [ ] Coverage for `pkg/vault/` >= 80%
- [ ] Step report written

---

## Step 8c: SIEMEmitter

**Branch:** `phase6/siem-emitter` (on `atlax-enterprise` repo)
**Depends on:** Step 6 (repo structure), Steps 7a+7b complete

### Context Brief

The community `SlogEmitter` writes audit events as structured JSON log entries. The enterprise `SIEMEmitter` forwards audit events to an event bus (Kafka or NATS) for SIEM integration (Splunk, Datadog, ELK). Events are serialized to JSON and published to a configurable topic. The emitter uses an async buffer (like `SlogEmitter`) with configurable flush interval and batch size for throughput.

The `SIEMEmitter` satisfies `audit.Emitter`. It implements `Emit` and `Close`.

### Tasks

#### 8c.1: Event bus client dependencies

- [ ] Add `github.com/segmentio/kafka-go` to enterprise `go.mod` (Kafka client)
- [ ] OR add `github.com/nats-io/nats.go` (NATS client) -- decision: support both via a `Publisher` interface
- [ ] Run `go mod tidy`

#### 8c.2: pkg/siem/emitter.go -- SIEMEmitter implementation

- [ ] `type Publisher interface` -- abstraction over Kafka/NATS:
  - `Publish(ctx context.Context, topic string, key string, value []byte) error`
  - `Close() error`
- [ ] `type KafkaPublisher struct` satisfying `Publisher`:
  - Wraps `kafka.Writer`
  - Topic from config
- [ ] `type NATSPublisher struct` satisfying `Publisher`:
  - Wraps `nats.Conn`
  - Subject from config
- [ ] `type SIEMEmitter struct` with fields:
  - `publisher Publisher`
  - `topic string`
  - `eventCh chan audit.Event`
  - `closeCh chan struct{}`
  - `done chan struct{}`
  - `once sync.Once`
  - `batchSize int` -- flush after this many events (default 100)
  - `flushInterval time.Duration` -- flush after this duration (default 1 second)
  - `logger *slog.Logger`
- [ ] Compile-time interface check: `var _ audit.Emitter = (*SIEMEmitter)(nil)`
- [ ] `func NewSIEMEmitter(publisher Publisher, topic string, bufferSize int, logger *slog.Logger) *SIEMEmitter`:
  1. Create buffered channel
  2. Start drain loop goroutine
  3. Return emitter
- [ ] `func (e *SIEMEmitter) Emit(ctx context.Context, event audit.Event) error`:
  1. Check if closed (same pattern as `SlogEmitter.Emit`)
  2. Send event to channel (non-blocking with closed check)
- [ ] `func (e *SIEMEmitter) Close() error`:
  1. Signal close (same pattern as `SlogEmitter.Close`)
  2. Wait for drain loop to flush remaining events
  3. Call `publisher.Close()`
- [ ] Drain loop:
  1. Batch events from channel up to `batchSize` or `flushInterval`
  2. Serialize each event to JSON
  3. Publish batch via `publisher.Publish`
  4. On publish failure: log error, do not drop events (retry with backoff up to 3 attempts, then drop with error log)
  5. On channel close: flush remaining events and exit

#### 8c.3: pkg/siem/emitter_test.go -- tests with in-memory publisher

- [ ] `type MockPublisher struct` that records published messages in a slice
- [ ] Test: Emit single event, verify it appears in mock publisher output
- [ ] Test: Emit multiple events, verify batch delivery
- [ ] Test: Close flushes remaining events before returning
- [ ] Test: Emit after Close returns `audit.ErrEmitterClosed`
- [ ] Test: Publisher failure triggers retry (mock publisher fails first call, succeeds second)
- [ ] Test: JSON serialization matches expected format (action, actor, target, timestamp, customer_id, metadata)
- [ ] Test: concurrent Emit from multiple goroutines (race detector validation)

#### 8c.4: Enterprise wiring

- [ ] Add SIEM config to `SIEMConfig` struct:
  - `Enabled bool`, `Backend string` (kafka or nats)
  - `Kafka KafkaConfig`: `Brokers []string`, `Topic string`, `TLSEnabled bool`, `SASLUsername string`, `SASLPassword string`
  - `NATS NATSConfig`: `URL string`, `Subject string`, `CredsFile string`
  - `BufferSize int`, `BatchSize int`, `FlushInterval time.Duration`
- [ ] In enterprise `cmd/relay/main.go`: replace `audit.NewSlogEmitter` with:
  1. If SIEM config enabled with backend "kafka": create `KafkaPublisher`, create `SIEMEmitter`
  2. If SIEM config enabled with backend "nats": create `NATSPublisher`, create `SIEMEmitter`
  3. If SIEM config not enabled: fall back to `audit.NewSlogEmitter` (community behavior)
- [ ] In enterprise `cmd/agent/main.go`: same wiring pattern (agents also emit audit events)

### Exit Criteria

- [ ] `var _ audit.Emitter = (*SIEMEmitter)(nil)` compiles
- [ ] Events forwarded to Kafka/NATS topic in JSON format
- [ ] Batch delivery with configurable size and flush interval
- [ ] Retry on publisher failure (up to 3 attempts)
- [ ] Close flushes remaining events
- [ ] Enterprise binary falls back to SlogEmitter when SIEM config is absent
- [ ] All tests pass with mock publisher (no real Kafka/NATS required for CI)
- [ ] Coverage for `pkg/siem/` >= 80%
- [ ] Step report written

---

## Anti-Pattern Checklist

| Anti-Pattern | Mitigation |
|---|---|
| Enterprise code leaks into community repo | Separate repos; community has no import of enterprise module; CI verifies community builds independently |
| Interface breaking change breaks enterprise | Enterprise CI builds against community module; any community interface change that removes or modifies a method breaks the enterprise build immediately |
| Build tags for edition switching | No build tags; two separate `main.go` files in two separate repos |
| Feature flags for enterprise | No flags; edition is determined by which binary runs |
| Self-update without signature verification | Ed25519 mandatory; public key embedded at compile time; SHA-256 + size verification on download |
| Fd passing race condition | New process signals readiness on a pipe before old process stops accepting; old process does not exit until readiness confirmed |
| SIGUSR2 during SIGUSR2 | Restarter holds a mutex; second SIGUSR2 is no-op while restart is in progress |
| Redis connection failure crashes relay | Fall back to MemoryRegistry with log warning; or fail fast at startup if Redis is explicitly configured (operator intent) |
| Vault unavailable during cert load | Return error (fail fast); do not silently use expired cert |
| Kafka/NATS unavailable during emit | Retry up to 3 times with backoff; drop event with error log after retries exhausted; never block the caller indefinitely |
| Miniredis/mock tests pass but real Redis fails | Integration test target in Makefile (`make test-integration`) that requires real Redis/Vault/Kafka (optional, not in CI) |
| go.work checked into repo | `.gitignore` in both repos excludes `go.work` and `go.work.sum`; go.work is developer-local only |
| Enterprise binary named same as community | Enterprise binaries are `atlax-relay-enterprise` and `atlax-agent-enterprise` (distinct names) |
| GOPRIVATE not set in CI | GitHub Actions workflow sets `GOPRIVATE=github.com/atlasshare/*` and configures git credential helper |

---

## Plan Mutation Protocol

All mutations to this plan are recorded with timestamp and rationale. Append to this section; never edit past entries.

| Date | Mutation | Rationale |
|------|----------|-----------|
| -- | -- | -- |

---

## Execution Log

| Step | Status | PR | Date |
|------|--------|----|------|
| Step 6: Enterprise repo setup + interface contract | NOT STARTED | -- | -- |
| Step 7a: Zero-downtime relay binary swap | NOT STARTED | -- | -- |
| Step 7b: Agent self-update | NOT STARTED | -- | -- |
| Step 8a: RedisRegistry | NOT STARTED | -- | -- |
| Step 8b: VaultStore | NOT STARTED | -- | -- |
| Step 8c: SIEMEmitter | NOT STARTED | -- | -- |

---

*Generated: 2026-04-03*
*Blueprint version: 1.0*
*Objective: Enterprise repo initialization, zero-downtime features, and distributed infrastructure (Phase 6, Steps 6-8)*
*Predecessor: plans/phase6-step5-community-release-plan.md (Phase 6, Step 5, v0.1.0)*
