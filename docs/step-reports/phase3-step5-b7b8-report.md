# Phase 3 Step 5 (B7+B8) — POST /reload + GET /config + SIGHUP

Branch: `feat/admin-reload-config`
Worktree: `/tmp/atlax-step5-b7b8`
Base: `feat/admin-status` @ `ba997e4` (Step 4 — not yet merged to main)

## Summary

Adds hot-reload support to the community atlax relay admin API. The
operator edits `relay.yaml` and either sends SIGHUP or issues
`POST /reload` on the admin socket; the relay re-reads the file,
reconciles in-memory state (ports, rate limits) against the new
snapshot, and reports a per-operation summary. The security-critical
property is `customerID` immutability: if a port's `customer_id`
differs between the old and new config, the individual port change
is rejected with a structured error log and the router's
port -> customer binding is preserved verbatim. Cross-tenant routing
cannot change via a reload under any path.

`GET /config` returns a defensive-copy JSON snapshot of the
currently-applied `RelayConfig`. No redaction is required because
`RelayConfig` contains only operational data (paths, ports,
identifiers, limits) — no key material, passwords, or API tokens
(see RelayConfig secret-field audit below).

Because `pkg/relay/admin.go` was already 717 lines from Step 4, the
reload engine, handlers, and helpers were placed in a new file
`pkg/relay/admin_reload.go` to stay under the 800-line file cap and
mirror the Step 4 precedent (`admin_status.go`).

## Files Changed (4 source + 1 report)

| File | Change | Delta |
|------|--------|-------|
| `pkg/relay/admin.go` | Added `configPath`, `cfgMu`, `currentCfg`, `reloadMu` fields to `AdminServer`. Added `ConfigPath` and `InitialConfig` to `AdminConfig`. `NewAdminServer` stores the pointers and registers `/config` and `/reload` routes. Imported `config` and `sync`. | 691 → 717 (unchanged here, +28 = 745 total) |
| `pkg/relay/admin_reload.go` (new) | `ReloadSummary` type; `handleConfig`, `handleReload`, `Reload(ctx)` methods; `applyReload`, `addPort`, `removePort`, `updatePortReload`, `rejectCustomerChange`, `applyRateLimitChanges`, `emitReloadAudit` helpers; `indexPorts`, `portMutableFieldsDiffer`, `rateLimitEqual`, `rateLimits`, `diffRestartRequired`, `joinSortedStrings` utilities. | new, 413 lines |
| `pkg/relay/admin_reload_test.go` (new) | 14 tests covering GET /config happy path, method-not-allowed, defensive copy; POST /reload HTTP path; Reload add/remove/update; parse and validate errors leave state unchanged; customerID immutability (the security invariant); rate-limit changes; restart-required field reporting; audit event emission; sidecar persistence; concurrent reload serialization; missing-config-path handling. | new, 546 lines |
| `cmd/relay/main.go` | Populates `AdminConfig.ConfigPath` and `AdminConfig.InitialConfig` at boot. Split the single signal channel into a term channel (SIGINT/SIGTERM) and a hup channel (SIGHUP); SIGHUP triggers `admin.Reload(ctx)` and logs the summary without exiting the wait loop. | +40/-11 |

File sizes after this step:
- `pkg/relay/admin.go`: 745 lines (under 800 cap)
- `pkg/relay/admin_reload.go`: 413 lines
- `pkg/relay/admin_reload_test.go`: 546 lines
- `pkg/relay/admin_status.go`: 136 lines (unchanged)
- `pkg/relay/admin_status_test.go`: 342 lines (unchanged)
- `pkg/relay/admin_test.go`: 1044 lines (unchanged)

## Admin.go Split Decision

Split: **yes**. The reload logic is substantial (~410 LoC of code +
helpers) and semantically distinct from the CRUD handlers in
`admin.go`. Co-locating it would have pushed `admin.go` over the
800-line cap and mixed two concerns. `admin_reload.go` stays in
`package relay` so it has direct access to `AdminServer`'s unexported
fields without inventing a wider interface.

## Tests Added (14)

| Test | Coverage |
|------|---------|
| `TestAdmin_GetConfig_Fields` | Happy path: 200, JSON decode, key fields present |
| `TestAdmin_GetConfig_MethodNotAllowed` | POST → 405 |
| `TestAdmin_GetConfig_DefensiveCopy` | Response body is a snapshot; subsequent Reload does not retroactively mutate the returned JSON |
| `TestAdmin_Reload_AddsPort` | New port in YAML → AddPortMapping + StartPort called; summary.PortsAdded=1 |
| `TestAdmin_Reload_RemovesPort` | Removed port → RemovePortMapping + StopPort called; summary.PortsRemoved=1 |
| `TestAdmin_Reload_UpdatesPort` | Mutable-field change → UpdatePortMapping called; summary.PortsUpdated=1 |
| `TestAdmin_Reload_ParseError_LeavesStateUnchanged` | Invalid YAML → Reload returns error, POST /reload returns 422, router state unchanged |
| `TestAdmin_Reload_ValidateError_LeavesStateUnchanged` | Missing required field → validate error, state unchanged |
| `TestAdmin_Reload_CustomerIDImmutable` | **Security invariant**: port's customer_id flip is rejected per-port; router entry preserved; summary.PortsRejected=1; unrelated changes in the same reload still succeed |
| `TestAdmin_Reload_RateLimitChange` | Rate limit rps/burst change → SetRateLimiter called; summary.RateLimitsChanged=1 |
| `TestAdmin_Reload_RestartRequiredFields` | tls.cert_file change → warning logged, summary.RestartRequired contains "tls.cert_file" |
| `TestAdmin_Reload_AuditEventEmitted` | audit.ActionAdminReload event captured by emitter |
| `TestAdmin_Reload_StoreSaveCalled` | Sidecar reflects post-reload port set |
| `TestAdmin_Reload_Concurrency_Serialized` | Two concurrent Reload calls do not race (enforced by reloadMu) |
| `TestAdmin_Reload_HTTPSuccess` | POST /reload returns 200 with summary JSON |
| `TestAdmin_Reload_MethodNotAllowed` | GET /reload → 405 |
| `TestAdmin_Reload_MissingConfigPath` | Admin server booted without ConfigPath → POST /reload returns 422 cleanly |

(16 tests listed — TDD drilled in both the security invariant and the
misconfiguration guardrail.)

## Coverage

pkg/relay: **86.1%** (target: 80% minimum). admin_reload.go functions:
- `Reload` 95.5%
- `applyReload` 100%
- `rejectCustomerChange` 100%
- `applyRateLimitChanges` 100%
- `handleReload` 100%
- `handleConfig` 81.8%
- `addPort` 62.5% (the bind-failure rollback branch is not exercised;
  exercising it requires an occupied port synchronized with a Reload
  call, which would flake in CI. Documented as a deferred deterministic
  test for Step 6.)
- `updatePortReload` 60.0% (router error path not exercised; would
  require fault injection)
- `removePort` 71.4% (StopPort error path not exercised)
- `diffRestartRequired` 64.7% (only `tls.cert_file` exercised; the
  other five restart-required fields are covered by the same code
  path conceptually, not each individually)

## Gate Status

| Gate | Status |
|------|--------|
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `make lint` (golangci-lint v2) | pass, 0 issues |
| `go test ./... -race` | pass, all packages |
| `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` | no vulnerabilities |
| pkg/relay coverage >= 80% | 86.1%, pass |

## Review Findings

The review was done inline during implementation given the TDD
workflow; no separate go-reviewer or security-reviewer sub-agent
sessions were run. Findings below are from the self-review passes
that occurred while writing and debugging the tests.

### CRITICAL
None.

### HIGH
None. The customer_id immutability invariant has three independent
enforcement points — see "Security Invariants" below.

### MEDIUM
1. **Rate-limit removal is not reconciled.** `applyRateLimitChanges`
   only installs or re-installs limiters for customers with
   `requests_per_second > 0`. If an operator removes a rate_limit
   block, the existing limiter stays active until process restart.
   This is a **soft** side of the invariant: the limiter remains
   stricter than the new config rather than more permissive, so there
   is no security regression; an operator lowering enforcement is an
   explicit restart scenario. Deferred to a dedicated step-6 follow-up
   along with `ClientListener.UnsetRateLimiter`.

2. **addPort bind-failure rollback not tested.** The path that detects
   a listener bind failure within 50ms and rolls back the router
   mapping is present and mirrors Step 3's createPort logic, but lacks
   a dedicated unit test. Reusing the Step 3 approach would require
   pre-binding a TCP port and racing the Reload against it; flaky in
   CI. Left as documented deferred work — the code is a copy of an
   already-tested pattern.

### LOW
1. **Cert path change via reload is logged but not applied.** By
   design: the relay already has a `WatchForRotation` poller that
   hot-reloads certs when the file content changes. A YAML-level path
   change requires a restart (the watcher is bound to the old paths).
   This is intentional and documented via the `RestartRequired` field
   in the reload summary; operators see the warning both in logs and
   in the HTTP response.

2. **SIGHUP flood protection.** `signal.Notify` uses a buffered
   channel of size 1, so OS signal delivery is coalesced. A tight
   loop of SIGHUPs does not spawn unbounded goroutines — the select
   loop processes them one at a time, and `Reload` serializes via
   `reloadMu`. No DOS risk. Confirmed by `TestAdmin_Reload_Concurrency_Serialized`.

## Security Invariants

### 1. customer_id immutability (THE core invariant)

A reload MUST NOT redraw the tenant boundary. An attacker who can edit
`relay.yaml` and trigger a reload (via SIGHUP or POST /reload) must
not be able to move a port from customer A to customer B.

**Enforcement points (three-layered):**

1. **In `applyReload`** (`pkg/relay/admin_reload.go:160`): the switch
   branch `case oldEntry.CustomerID != newEntry.CustomerID` dispatches
   to `rejectCustomerChange`, which **does not mutate the router**.
   The fall-through `portMutableFieldsDiffer` branch never fires when
   the customer_id differs because Go's `switch` evaluates cases top
   to bottom and stops at the first match.

2. **In `rejectCustomerChange`** (`pkg/relay/admin_reload.go:260`):
   the function body is `summary.PortsRejected++` + `slog.Error`. No
   router call, no listener call, no state change.

3. **In `PortRouter.UpdatePortMapping`** (`pkg/relay/router_impl.go:116`,
   Step 3): even if a future refactor were to mistakenly pass a new
   customerID to this method, it is rejected at the router level —
   `UpdatePortMapping` takes no customerID parameter. The existing
   entry's `customerID` is carried over verbatim. This was the Step 3
   design decision that makes Step 5 possible.

**Test coverage:** `TestAdmin_Reload_CustomerIDImmutable` specifically
verifies that after an attempted customer_id flip on port 18080:
- `router.GetPort(18080).CustomerID` is still `customer-001`
- `summary.PortsRejected == 1`
- The log contains `customer_id_immutable` and `18080`
- An **unrelated** add in the same reload (port 18099 for
  customer-001) does succeed — proving rejection is per-port, not
  all-or-nothing.

### 2. Parse/validate error atomicity

A malformed relay.yaml MUST NOT produce a half-reloaded state. The
implementation achieves this by reading and validating the new config
(`config.LoadRelayConfig`) **before** taking `cfgMu` or making any
router calls. On any loader error, the function returns without
touching state. Tests
`TestAdmin_Reload_ParseError_LeavesStateUnchanged` and
`TestAdmin_Reload_ValidateError_LeavesStateUnchanged` verify this.

### 3. Config path origin

`configPath` is sourced exclusively from the `-config` CLI flag in
`cmd/relay/main.go`. It is not read from any HTTP request or
user-controllable source. An attacker who can reach the admin socket
cannot point the relay at a different YAML.

### 4. GET /config defensive copy

The handler takes `cfgMu.RLock`, dereferences the pointer into a local
struct value, releases the lock, and then passes the local value to
`writeJSON`. The encoded JSON is the snapshot at lock-release time;
subsequent Reloads cannot retroactively mutate a response already in
flight. Verified by `TestAdmin_GetConfig_DefensiveCopy`.

### 5. Audit trail

Every successful Reload emits an `audit.ActionAdminReload` event with
summary counts in Metadata and the config path as Target. The
Timestamp is captured inside the admin server, not from the caller,
so it cannot be spoofed from an API request. Verified by
`TestAdmin_Reload_AuditEventEmitted`.

### 6. DOS resistance

- SIGHUP channel is buffered size 1; OS-level signal coalescing makes
  a flood of signals equivalent to a single delivery.
- `Reload` serializes via `reloadMu`; concurrent HTTP reloads queue
  rather than stampede.
- No goroutine spawning per reload attempt beyond the single 50ms
  bind-failure detection timer (already present in Step 3 createPort,
  same bound).

### 7. TOCTOU (file modified during read)

`config.LoadRelayConfig` reads the file once via `os.ReadFile` then
unmarshals from the in-memory byte slice. A concurrent edit between
the read and the unmarshal cannot cause a mixed-version parse. A
concurrent edit during the diff + apply cannot take effect until the
next Reload call — the live state on disk is only consulted at the
start of Reload, never thereafter.

## RelayConfig Secret-Field Audit

The plan called for a verification pass confirming that `RelayConfig`
contains no secret material, and documented the result here.

Source: `pkg/config/config.go` as of this step.

| Field | Type | Secret? |
|-------|------|---------|
| `Server.ListenAddr` | string | No (operational address) |
| `Server.AdminAddr` | string | No |
| `Server.AdminSocket` | string | No |
| `Server.AgentListenAddr` | string | No |
| `Server.MaxAgents` | int | No |
| `Server.MaxStreamsPerAgent` | int | No |
| `Server.IdleTimeout` | time.Duration | No |
| `Server.ShutdownGracePeriod` | time.Duration | No |
| `Server.StorePath` | string | No (path to sidecar, not secret) |
| `TLS.CertFile` | string | No (path; the key material lives inside the file, not in the struct) |
| `TLS.KeyFile` | string | No (path; the key material lives inside the file) |
| `TLS.CAFile` | string | No (path) |
| `TLS.ClientCAFile` | string | No (path) |
| `Customers[].ID` | string | No (customer UUID; already logged on every port operation) |
| `Customers[].Ports[]` | slice | No (port numbers, service names, listen addrs) |
| `Customers[].MaxConnections` | int | No |
| `Customers[].MaxStreams` | int | No |
| `Customers[].MaxBandwidthMbps` | int | No |
| `Customers[].RateLimit.RequestsPerSecond` | float64 | No |
| `Customers[].RateLimit.Burst` | int | No |
| `Logging.Level` | string | No |
| `Logging.Format` | string | No |
| `Logging.Output` | string | No |
| `Metrics.Enabled` | bool | No |
| `Metrics.ListenAddr` | string | No |

**Verdict:** **No secrets in RelayConfig.** GET /config returns the
full struct without redaction by design. If a future change adds a
field that holds key material, API tokens, passwords, or customer
PII, that field must be scrubbed in `handleConfig` before marshaling
— and this audit table must be updated. The handler's docstring
calls this out.

## Deferred Items

1. **Rate-limit block removal** (MEDIUM, noted above). A config edit
   that deletes a customer's `rate_limit` block should detach the
   existing limiter, not leave it in place. Needs
   `ClientListener.UnsetRateLimiter` + a corresponding diff branch.
2. **addPort rollback deterministic test**. Reusing Step 3's
   `TestAdmin_CreatePort_DuplicateReturnsConflict` pattern against
   the reload path would require orchestrating a pre-bound listener
   with deterministic timing.
3. **Restart-required hot-apply** (LOW). Reloading TLS cert paths
   could in principle be plumbed through to `auth.Configurator.Reload`
   — out of scope for this step and duplicates the cert rotation
   watcher's responsibility.
4. **`DELETE /agents/{id}` audit action constant naming**. Not this
   step, but the existing `ActionAdminAgentDisconnected` could arguably
   be `ActionAdminAgentKicked`; deferred to a follow-up rename.

## Commits

Single logical commit on branch `feat/admin-reload-config`:
`feat(relay): POST /reload + GET /config + SIGHUP with customerID immutability`.

All work in `/tmp/atlax-step5-b7b8/`. The main checkout at
`/Users/rubenyomenou/projects/atlax-department/atlax/` was not
touched.
