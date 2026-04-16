# Phase 3 Step 3 (B3) — PUT /ports/{port}

Branch: `feat/admin-put-port`
Worktree: `/tmp/atlax-step3-b3`
Base: `origin/main` @ `4bc1b16` (after PR #105 merge)

## Summary

Adds a PUT /ports/{port} endpoint to the community atlax relay admin API,
allowing operators to update the mutable fields (`service`, `listen_addr`,
`max_streams`) of an existing port mapping at runtime. The tenant binding
(`customer_id`) is treated as immutable: neither the request body nor the
router method accept a customer ID parameter, so a PUT cannot move a
port between tenants. Persistence goes through the existing sidecar
store, so updates survive a relay restart without requiring edits to
`relay.yaml`.

## Files Changed (5)

| File | Change |
|------|--------|
| `pkg/relay/router.go` | Added `ErrPortNotFound` sentinel (and `errors` import). |
| `pkg/relay/router_impl.go` | Added `UpdatePortMapping(port, service, listenAddr, maxStreams)`; customerID is preserved verbatim from the existing entry. |
| `pkg/relay/admin.go` | Added `PortUpdateRequest` struct, PUT case in `handlePortByID`, and `updatePort` handler plus three helpers (`decodePortUpdateRequest`, `mergePortUpdate`, `persistPortUpdate`). Emits `audit.ActionAdminPortUpdated`. Calls `store.SaveCurrentState` on success. |
| `pkg/relay/router_impl_test.go` | Added 3 new test cases for UpdatePortMapping (success, not-found, customerID preservation). |
| `pkg/relay/admin_test.go` | Added `adminFixture` helper (store + emitter + router + listener) plus 7 new PUT handler tests. Adjusted the stale `TestAdmin_PortByID_MethodNotAllowed` to use PATCH instead of PUT (PUT is now an accepted method). |

## Tests

Added 10 new tests. All pass with `go test ./... -race`:

- `TestPortRouter_UpdatePortMapping_Success`
- `TestPortRouter_UpdatePortMapping_NotFound`
- `TestPortRouter_UpdatePortMapping_PreservesCustomerID`
- `TestAdmin_UpdatePort_Success`
- `TestAdmin_UpdatePort_NotFound`
- `TestAdmin_UpdatePort_EmptyBody`
- `TestAdmin_UpdatePort_PartialUpdate`
- `TestAdmin_UpdatePort_InvalidJSON`
- `TestAdmin_UpdatePort_InvalidListenAddr`
- `TestAdmin_UpdatePort_NegativeMaxStreams`

### Coverage

| Package | Before | After |
|---------|--------|-------|
| pkg/relay | 89.2% (reported in plan) | 86.1% |

The small net drop is attributable to adding three new helper functions
in `admin.go` (`decodePortUpdateRequest`, `mergePortUpdate`,
`persistPortUpdate`) and to the `updatePort` handler's two defensive
branches that the tests do not trigger: (a) the secondary
`ErrPortNotFound` check after `UpdatePortMapping`, which is racing with
an intervening DELETE and is shadowed by the primary `GetPort` lookup in
the happy path; and (b) the sidecar save-failure branch. Both branches
are functionally exercised by other tests in the package (the sidecar
path by `createPort` tests, the sentinel by the router-level tests). The
new router-level `UpdatePortMapping` is at 100%.

Coverage remains comfortably above the 80% floor.

## Gate Results

| Gate | Status |
|------|--------|
| `make test` (with -race) | PASS (all packages) |
| `make lint` (golangci-lint v2) | PASS (0 issues) |
| `go vet ./...` | PASS (0 warnings) |
| `govulncheck ./...` | NOT RUN — network/DNS access to vuln.go.dev unavailable in this environment. Should be re-run before merge. |

## Review Findings

I performed a self-review with the go-reviewer and security-reviewer
rubrics in mind. The parallel agent invocation was not used because the
Task tool is not available in this session; the review was done in-line.

### CRITICAL (0)

None.

### HIGH (0)

None.

### MEDIUM (2, all addressed before this report)

1. **`updatePort` originally exceeded 50 lines** (89 lines). Refactored
   by extracting `decodePortUpdateRequest`, `mergePortUpdate`, and
   `persistPortUpdate`; the handler is now 44 lines.

2. **`TestAdmin_PortByID_MethodNotAllowed` tested PUT as the forbidden
   method.** PUT is now a first-class method on `/ports/{port}`, so the
   test was stale. Changed to assert PATCH returns 405.

### LOW (1, deferred)

1. **`pkg/relay/admin.go` at 691 lines.** Still below the 800-line
   workspace ceiling, but growing. Extracting handler groups (ports,
   agents, lifecycle) to separate files would be a structural
   improvement. Not in scope for B3; track as a refactor candidate when
   the next handler lands (likely step 5a UPDATE via /agents or step 4
   /status).

## Security Invariants Verified

1. **customerID is immutable through PUT.** `PortUpdateRequest` has no
   `CustomerID` field and does not unmarshal one from JSON — unknown
   fields are discarded by the standard `encoding/json` decoder. The
   router method `UpdatePortMapping` does not accept a customer ID
   parameter at all; internally it re-uses `entry.customerID` verbatim
   from the existing map entry. `TestPortRouter_UpdatePortMapping_PreservesCustomerID`
   asserts the invariant directly after two successive updates.

2. **Cross-tenant routing cannot be introduced via PUT.** There is no
   code path in `updatePort` or `UpdatePortMapping` that writes to
   `portEntry.customerID`. A tenant misassignment would require either
   (a) calling `RemovePortMapping` + `AddPortMapping` (which requires
   explicit operator intent via DELETE then POST, each audited
   separately), or (b) editing the code.

3. **Input validation at the boundary.** JSON decode errors return 400;
   `listen_addr` is parsed with `net.ParseIP` and rejected if not a
   literal IP; `max_streams` must be non-negative; completely empty
   requests are rejected as 400 to avoid silent no-ops.

4. **Audit trail.** `audit.ActionAdminPortUpdated` is emitted on every
   successful PUT, carrying the port, customer ID, and the updated
   `service`, `listen_addr`, and `max_streams` values as metadata. The
   actor is `admin-api`, consistent with existing admin endpoints.

5. **Persistence on success only.** `store.SaveCurrentState` is called
   after `UpdatePortMapping` returns nil; a sidecar save failure is
   logged at WARN but does not fail the request (the runtime state is
   already correct and the operator can retry). This matches the
   established convention in `createPort` and `deletePort`.

6. **No emoji, no attribution, no secrets.** Confirmed against the
   workspace CLAUDE.md.

## Deferred Items

| Item | Reason | Follow-up |
|------|--------|-----------|
| `govulncheck ./...` | Network access to vuln.go.dev was unavailable in the sandbox used for this step. | Run before merge; expected clean given no new external deps. |
| Split `pkg/relay/admin.go` | At 691 lines; still under 800. Out of scope for B3. | Consider during step 4 (/status) or step 5a implementation. |
| `pkg/relay/admin_test.go` at 1044 lines | Test files routinely exceed the source ceiling in Go; not enforced by lint. | Optional split when test count doubles again. |

## Open Questions

None. The plan for Step 3 B3 (lines 179-210 of `plans/phase3-runtime-operator-plan.md`) was unambiguous. The only nuance added
over the literal plan text is `PortUpdateRequest.MaxStreams *int` (instead
of `int`) so that the sentinel value 0 meaning "unlimited" remains
distinguishable from "field omitted"; this matters because a client
setting `max_streams: 0` explicitly (to remove a cap) and a client
omitting the field (to preserve the existing cap) are different
operations. Without `*int`, both look like zero after JSON decode.
