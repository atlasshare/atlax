# Blueprint: Phase 6, Step 5 -- Community Release Prep (v0.1.0)

**Objective:** Fix two functional gaps in the community edition (admin API port lifecycle, relay test coverage), update stale documentation, and tag the first versioned release. After this step, `atlax` is a stable dependency for the `atlax-enterprise` module.

**Status:** NOT STARTED
**Target duration:** 1-2 sessions
**Estimated sessions:** 2-3

**Prerequisites:** Phase 6 Steps 1-4 complete (PRs #72-#75 merged to main).

**Related issues:** #40 (port lifecycle gap, partially addressed in Step 4)

---

## Scope

### In scope

| Item | Sub-step |
|------|----------|
| `StopPort` method on ClientListener | 5a |
| Admin API `createPort` wires `StartPort` | 5a |
| Admin API `deletePort` wires `StopPort` | 5a |
| `PortCreateRequest.ListenAddr` field (default `0.0.0.0`) | 5a |
| Admin API port lifecycle tests | 5a |
| `pkg/relay` coverage from 72% to 80%+ | 5b |
| Rewrite stale `docs/api/control-plane.md` | 5c |
| Update Phase 6 execution log (Steps 1-4 marked complete) | 5c |
| Tag `v0.1.0` on main | 5d |
| GitHub release with release notes | 5d |

### Deferred (not this step)

| Item | Why |
|------|-----|
| Pre-built release binaries | Step 7 (enterprise separation) |
| Docker Hub push | Step 7 |
| Enterprise repo initialization | Step 6+ |

---

## Dependency Graph

```
Step 5a (Admin API port lifecycle fix)
   |
   v
Step 5b (Relay coverage to 80%+)  [depends on 5a: new code needs tests too]
   |
   v
Step 5c (Docs + execution log update)  [depends on 5a: docs describe fixed behavior]
   |
   v
Step 5d (Tag v0.1.0 + GitHub release)  [depends on all]
```

---

## Invariants (verified after EVERY sub-step)

1. `go build ./...` passes
2. `go vet ./...` passes
3. `go test -race ./...` passes
4. `golangci-lint run ./...` passes
5. Coverage for `pkg/relay` >= 80%
6. No function > 50 lines, no file > 800 lines
7. Step report written immediately

---

## Step 5a: Admin API Port Lifecycle Fix -- TDD

**Branch:** `phase6/port-lifecycle`
**Depends on:** main (Steps 1-4 merged)
**Serial:** yes

### Context Brief

`POST /ports` in `admin.go:263` calls `router.AddPortMapping` but does not call `clientListener.StartPort`. This means runtime port addition creates a routing entry but no TCP listener -- clients cannot connect. Similarly, `DELETE /ports/{port}` removes the routing entry but does not close the listener. This is the remaining gap from Step 4 (#40).

The fix requires:
1. A new `StopPort(port int) error` method on `ClientListener` (mirror of `StartPort`)
2. Wiring `StartPort` into `createPort` with a long-lived context (not the HTTP request context)
3. Wiring `StopPort` into `deletePort`
4. Adding `ListenAddr` to `PortCreateRequest` for parity with YAML config

### Tasks

#### ClientListener.StopPort

- [ ] Add `StopPort(port int) error` to `client_listener.go`
  - Acquire `cl.mu` lock
  - Look up `cl.listeners[port]`; return error if not found
  - Call `ln.Close()` on the listener
  - Delete entry from `cl.listeners`
  - Log the stop event
  - Follows existing `Stop()` pattern (lines 137-151)

#### AdminServer lifecycle context

- [ ] Add `ctx context.Context` field to `AdminServer` struct
- [ ] Store `ctx` in `Start()` before the goroutine (line 120): `a.ctx = ctx`
- [ ] New port listener goroutines derive from `a.ctx` so they live beyond the HTTP request

#### Wire StartPort into createPort

- [ ] Add `ListenAddr string` to `PortCreateRequest` (default `"0.0.0.0"` if empty)
- [ ] After successful `router.AddPortMapping` in `createPort` (line 275):
  1. Build addr: `fmt.Sprintf("%s:%d", listenAddr, req.Port)`
  2. Launch `go cl.StartPort(a.ctx, addr, req.Port)` in a goroutine
  3. Brief sleep + `cl.Addr(req.Port)` check to confirm listener started
  4. On failure: rollback `router.RemovePortMapping`, return HTTP 500
- [ ] Log the listener start

#### Wire StopPort into deletePort

- [ ] After `router.RemovePortMapping` in `deletePort` (line 300):
  1. Call `a.clientListener.StopPort(port)`
  2. If error (port was config-started, not admin-started): log warning, do not fail HTTP response
- [ ] Log the listener stop

#### Tests (TDD -- write first)

- [ ] `TestClientListener_StopPort_Success` -- start then stop, verify `Addr(port)` returns nil
- [ ] `TestClientListener_StopPort_NotFound` -- stop a never-started port, verify error
- [ ] `TestAdmin_CreatePort_StartsListener` -- POST /ports, then TCP connect to `clientListener.Addr(port)`
- [ ] `TestAdmin_DeletePort_StopsListener` -- POST then DELETE, verify `Addr(port)` is nil
- [ ] `TestAdmin_CreatePort_WithListenAddr` -- POST with `listen_addr: "127.0.0.1"`, verify bound to loopback
- [ ] Update `testAdminServer` helper to return `*ClientListener` (4th return value)

### Exit Criteria

- POST /ports creates routing entry AND starts TCP listener
- DELETE /ports/{port} removes routing entry AND stops TCP listener
- `ListenAddr` field supported in POST /ports
- All new code has tests
- Step report written

---

## Step 5b: Relay Package Coverage to 80%+ -- TDD

**Branch:** `phase6/relay-coverage`
**Depends on:** Step 5a (new StopPort code needs coverage)

### Context Brief

`pkg/relay` is at 72.0% coverage -- the only package below the 80% target. Per-function coverage analysis shows these 0% or low-coverage functions:

| Function | File | Coverage | Why untested |
|----------|------|----------|-------------|
| `handleClient` | client_listener.go:90 | 0% | Accept loop integration |
| `SetRateLimiter` | client_listener.go:39 | 0% | Called from cmd/relay |
| `Addr` | client_listener.go:154 | 0% | Utility, never called in tests |
| `Start` | admin.go:119 | 40.9% | Only TCP path tested, not unix socket |
| `serve` | admin.go:162 | 0% | Called by Start for unix socket |
| `writeError` | admin.go:364 | 0% | Only used by error branches |
| `handleAgentByID` | admin.go:339 | 50.0% | Missing error/method branches |
| `handleStats` | admin.go:192 | 58.3% | Missing method-not-allowed |
| `handleConnection` | listener.go:86 | 43.3% | mTLS integration path |
| `SetMetrics` | router_impl.go:38 | 0% | Called from cmd/relay |
| `SetMetrics` | registry_impl.go:41 | 0% | Called from cmd/relay |
| `SetCustomerLimit` | registry_impl.go:45 | 0% | Called from cmd/relay |
| `Addr` | server_impl.go:108 | 0% | Returns nil stub |

### Tasks

#### ClientListener handleClient paths (biggest coverage gap)

- [ ] `TestClientListener_HandleClient_NoMapping` -- call `handleClient` with a port that has no routing entry, verify conn is closed
- [ ] `TestClientListener_HandleClient_RateLimited` -- configure `SetRateLimiter`, connect multiple times exceeding burst, verify conn closed and metric incremented
- [ ] `TestClientListener_HandleClient_RouteFails` -- port mapped but no agent registered, verify error logged
- [ ] `TestClientListener_SetRateLimiter` -- verify limiter is created, verify rps <= 0 is no-op
- [ ] `TestClientListener_Addr` -- start port, verify Addr returns non-nil; verify Addr for unstated port returns nil

#### Admin server error paths

- [ ] `TestAdmin_Stats_MethodNotAllowed` -- PUT /stats returns 405
- [ ] `TestAdmin_Ports_MethodNotAllowed` -- PUT /ports returns 405
- [ ] `TestAdmin_Agents_MethodNotAllowed` -- POST /agents returns 405
- [ ] `TestAdmin_AgentByID_MethodNotAllowed` -- GET /agents/x returns 405
- [ ] `TestAdmin_AgentByID_EmptyID` -- DELETE /agents/ with trailing slash only, verify 400
- [ ] `TestAdmin_PortByID_InvalidPort` -- DELETE /ports/abc returns 400

#### Admin server unix socket paths

- [ ] `TestAdmin_StartUnixSocket` -- start on socket path (t.TempDir), HTTP request over unix socket succeeds
- [ ] `TestAdmin_StartBothTCPAndUnix` -- set both Addr and SocketPath, verify both work

#### Registry and router SetMetrics/SetCustomerLimit

- [ ] `TestMemoryRegistry_SetMetrics` -- verify SetMetrics stores the metrics reference
- [ ] `TestMemoryRegistry_SetCustomerLimit` -- verify limit stored, verify Register behavior at limit
- [ ] `TestPortRouter_SetMetrics` -- verify SetMetrics stores the metrics reference

#### Server stub

- [ ] `TestRelay_Addr_ReturnsNil` -- documents current behavior

### Exit Criteria

- `go test -race -coverprofile=cover.out ./pkg/relay/` reports >= 80%
- All new tests pass with `-race`
- No existing tests broken
- Step report written

---

## Step 5c: Documentation + Execution Log -- No Code

**Branch:** `docs/v0.1-release`
**Depends on:** Step 5a (docs describe fixed behavior)

### Tasks

#### Rewrite control-plane.md

- [ ] Replace `docs/api/control-plane.md` entirely to match actual implementation:
  - Transport: unix domain socket (`/var/run/atlax.sock`, 0660) + optional TCP
  - Authentication: none (secured by file permissions on socket)
  - No `/api/v1/` prefix, no `/readyz`, no mTLS admin auth, no rate limiting on admin API
  - Endpoints: `/healthz` GET, `/metrics` GET, `/stats` GET, `/ports` GET/POST, `/ports/{port}` DELETE, `/agents` GET, `/agents/{customerID}` DELETE
  - Request/response formats matching actual struct definitions
  - `POST /ports` now starts a TCP listener (fixed in Step 5a)
  - `DELETE /ports/{port}` now stops the TCP listener
  - Enterprise extension note: TCP + bearer token for remote fleet management

#### Update execution log

- [ ] Update `plans/phase6-operations-plan.md` execution log:
  ```
  | Step 1: Systemd + Docker      | COMPLETED | #72 | 2026-04-02 |
  | Step 2: Monitoring             | COMPLETED | #73 | 2026-04-02 |
  | Step 3: Security + perf        | COMPLETED | #74 | 2026-04-02 |
  | Step 4: Admin API              | COMPLETED | #75 | 2026-04-03 |
  | Step 5: Community release prep | COMPLETED | #XX | 2026-04-XX |
  ```

#### Update plan status

- [ ] Update `plans/phase6-operations-plan.md` status from "NOT STARTED" to reflect actual state
- [ ] Renumber original Steps 5-7 to Steps 6-8 in the plan (enterprise features now start at Step 6)
- [ ] Update Step 7 to reference `v0.1.0` instead of `v1.0.0-community`

### Exit Criteria

- `docs/api/control-plane.md` matches actual implementation
- Execution log accurate
- No stale references to v1.0.0-community (now v0.1.0)
- Step report written

---

## Step 5d: Tag v0.1.0 + GitHub Release

**Branch:** main (all PRs merged)
**Depends on:** Steps 5a-5c complete

### Tasks

#### Final verification

- [ ] `make test` -- all tests pass with `-race`
- [ ] `make lint` -- no lint errors
- [ ] `make build` -- both binaries build
- [ ] `go test -race -coverprofile=cover.out ./...` -- verify per-package coverage
- [ ] Verify no open issues blocking release

#### Tag

- [ ] `git tag -a v0.1.0 -m "v0.1.0: first tagged release"`
- [ ] `git push origin v0.1.0`

#### GitHub release

- [ ] `gh release create v0.1.0` with release notes:
  - One-paragraph description of atlax
  - What v0.1.0 includes (Phases 1-6 Step 5)
  - What v0.x means: pre-stable, interface changes possible before v1.0
  - Known limitations: single-relay, single-agent-per-customer, no TLS 1.2 fallback
  - Build instructions (`make build`)
  - No pre-built binaries (deferred to Step 7 enterprise separation)

### Exit Criteria

- `v0.1.0` tag exists on main
- GitHub release published with notes
- `go get github.com/atlasshare/atlax@v0.1.0` works
- Step report written

---

## Anti-Pattern Checklist

| Anti-Pattern | Mitigation |
|---|---|
| StartPort from HTTP handler blocks forever | Launch in goroutine with stored `a.ctx`, not request context |
| Race: config startup + POST /ports bind same port | `net.Listen` fails with "address in use"; admin handler rolls back |
| StopPort on config-started port fails | Log warning but succeed HTTP response; mapping was already removed |
| Coverage tests are fragile integration tests | Use `net.Pipe()` for handleClient tests; use `t.TempDir()` for unix socket |
| Tag before all PRs merged | Step 5d depends on 5a-5c; verification gate runs full test suite |
| v0.1.0 tag on broken commit | Final verification step runs make test + lint + build before tagging |

---

## Versioning Strategy

SemVer with `v0.x` pre-stable contract:

```
v0.1.0   -- this release
v0.1.1   -- patch (bug fix, no interface change)
v0.2.0   -- minor (new feature or interface addition)
...
v0.9.0   -- last minor before stability review
v1.0.0   -- stable: interfaces frozen, enterprise contract locked
```

Rules:
- `v0.x` = no stability promise. Breaking interface changes are expected.
- Enterprise repo pins exact versions: `require github.com/atlasshare/atlax v0.1.0`
- Patches are unbounded: v0.1.15 is valid (no cap at .9)
- `v1.0.0` = interface contract frozen. Breaking changes require `v2` (new import path per Go module rules).

---

## Execution Log

| Sub-step | Status | PR | Date |
|----------|--------|----|------|
| Step 5a: Port lifecycle fix | NOT STARTED | -- | -- |
| Step 5b: Relay coverage 80%+ | NOT STARTED | -- | -- |
| Step 5c: Docs + execution log | NOT STARTED | -- | -- |
| Step 5d: Tag v0.1.0 | NOT STARTED | -- | -- |

---

*Generated: 2026-04-03*
*Blueprint version: 1.0*
*Objective: Fix community gaps and tag v0.1.0 for enterprise dependency (Phase 6, Step 5)*
*Predecessor: Phase 6 Steps 1-4 (Operations track, completed 2026-04-03)*
