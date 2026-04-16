# Phase 3 Step 4 (F2) — GET /status

Branch: `feat/admin-status`
Worktree: `/tmp/atlax-step4-f2`
Base: `feat/admin-put-port` @ `88d317f` (Step 3 — not yet merged to main)

## Summary

Adds a GET /status endpoint to the community atlax relay admin API,
exposing a point-in-time snapshot of relay health, runtime counts, and
certificate expiry metadata for operator dashboards and the `ats` CLI.
The endpoint is GET-only, always returns 200 with `"status":"ok"` for
now (readiness downgrades are deferred), and tolerates missing or
malformed cert files by skipping the offending entry and logging a
warning. New `pkg/config.{Version,Commit,Date}` vars satisfy the
existing Makefile ldflags and surface the build-injected version on
`/status` as `config_version`.

Because `pkg/relay/admin.go` was already 691 lines before this step,
the new types and handler were placed in a separate file
`pkg/relay/admin_status.go` to stay under the 800-line file-size cap.
Only the `AdminServer` struct, `AdminConfig`, `NewAdminServer`, and the
route registration remain in `admin.go`.

## Files Changed (5 source + 1 report)

| File | Change |
|------|--------|
| `pkg/relay/admin.go` | Added `certPaths` and `configVersion` fields to `AdminServer`. Added `CertPaths` and `ConfigVersion` to `AdminConfig`. `NewAdminServer` now defensively copies `CertPaths` and registers `/status`. |
| `pkg/relay/admin_status.go` (new, 136 lines) | `StatusResponse`, `CertExpiry`, `CertNamePath` types; `handleStatus` handler; `collectRelayCerts` helper; `parseCertExpiry` helper. |
| `pkg/relay/admin_status_test.go` (new, 340 lines) | 15 new tests covering handler behavior, cert parsing edge cases, monotonic uptime, empty-slice JSON encoding, and defensive-copy smoke test. |
| `pkg/config/version.go` (new, 15 lines) | Mutable string vars `Version`, `Commit`, `Date` with `"dev"`/`"unknown"` defaults — the targets the Makefile ldflags already expected. |
| `cmd/relay/main.go` | Added `collectRelayCertPaths` helper. Populates `AdminConfig.CertPaths` (relay cert, relay CA, client CA — empty paths filtered) and `AdminConfig.ConfigVersion` from `config.Version`. |

File sizes after this step:
- `pkg/relay/admin.go`: 717 lines (up from 691, well under the 800 cap)
- `pkg/relay/admin_status.go`: 136 lines
- `pkg/relay/admin_status_test.go`: 340 lines

## File Split Justification

`admin.go` was already at 691 lines. Inlining the new types and handler
would have pushed it over 800, violating the file-size cap in the
workspace conventions. Splitting into `admin_status.go` keeps cohesion
(everything `/status`-related lives in one file) and isolates cert
parsing from the general admin API surface, which is a clean
responsibility boundary. Same package, so all AdminServer fields remain
accessible without exporting anything new.

## Tests

Added 15 new tests. All pass with `go test ./pkg/relay/... -race`:

Handler behavior:
- `TestAdmin_Status_Fields` — 200 OK, Content-Type, all 8 JSON keys present, typed decode
- `TestAdmin_Status_AgentCount` — registry wiring (0 agents, then 1 after register)
- `TestAdmin_Status_PortCount` — router wiring (3 ports added → ports_active=3)
- `TestAdmin_Status_RelayCerts` — valid cert → RFC3339 expires_at, days_left in [89,90]
- `TestAdmin_Status_CertMissing` — missing file skipped, 200 still returned
- `TestAdmin_Status_CertMalformed` — non-PEM file skipped, 200 still returned
- `TestAdmin_Status_UptimeMonotonic` — uptime_seconds non-decreasing across calls
- `TestAdmin_Status_MethodNotAllowed` — POST returns 405
- `TestAdmin_Status_NoCertsConfigured` — relay_certs serializes as `[]`, not `null`
- `TestAdmin_Status_DefensiveCopy` — two concurrent /status calls, both return full cert list (race-detector smoke)

Helper unit tests:
- `TestParseCertExpiry_Valid`
- `TestParseCertExpiry_MissingFile`
- `TestParseCertExpiry_MalformedPEM`
- `TestParseCertExpiry_ExpiredCert` — days_left can be ≤ 0 so operators can alert
- `TestParseCertExpiry_WrongPEMType` — e.g., PRIVATE KEY PEM is rejected

## Coverage

| Package | Before (Step 3 report) | After this step |
|---------|------------------------|-----------------|
| pkg/relay | 86.1% | 86.5% |

Per-function coverage on new code:
- `handleStatus`: 85.7%
- `collectRelayCerts`: 100.0%
- `parseCertExpiry`: 93.3%

The small rise (+0.4pp) comes from the new status tests exercising
registry.ListConnectedAgents and router.ListPorts in additional code
paths.

## Verification Gates

```
make lint           -> 0 issues
go vet ./...        -> clean
go test ./... -race -> all packages pass
make build          -> relay + agent binaries produced (ldflags inject real version)
govulncheck ./...   -> No vulnerabilities found
```

## Review Findings

### Self-review: go-reviewer perspective

| Severity | Finding | Resolution |
|----------|---------|------------|
| LOW | Cert parsing runs on every `/status` call (no cache). | Accepted. Parse is microseconds for a small PEM, filesystem caching keeps it fast, and re-reading each call means cert rotation is picked up immediately without a TTL. Documented inline. |
| LOW | `days_left` truncates to whole days via integer division on hours/24. For a cert expiring in 23 hours and 59 minutes this reports `0` days left rather than `1`. | Accepted. This is the conservative direction (alerts fire earlier). Documented as intentional in the `parseCertExpiry` comment. |
| INFO | `collectRelayCerts` returns a freshly allocated slice per call — confirmed by the `DefensiveCopy` test. | OK. |

No CRITICAL or HIGH.

### Self-review: security-reviewer perspective

| Severity | Finding | Resolution |
|----------|---------|------------|
| LOW (documented) | `os.ReadFile(path)` in `parseCertExpiry` uses a non-constant path (triggers gosec G304). | The path comes from `cfg.TLS.*` which is operator-trusted config, not user input. Annotated with `//nolint:gosec` and a comment noting the trust boundary. |
| INFO | `/status` info disclosure surface: status string, uptime, agent/stream/port counts, version, cert name + NotAfter. No key material, no cert contents, no subject/CN, no paths. | Intentional and safe. The endpoint is also bound to the unix socket (0o600) in the typical deployment. |
| INFO | DOS via repeated /status calls forcing cert re-parse. | Parse is fast; abuse would saturate the unix socket, not the relay. Caching was considered and rejected (see LOW above). |

No CRITICAL or HIGH.

## Security Considerations Addressed

- Cert paths read from trusted operator config; trust boundary annotated.
- No private key or cert content leaks — only `NotAfter` and the
  operator-supplied display name.
- Defensive copy of `CertPaths` in `NewAdminServer` prevents caller
  mutation races.
- `collectRelayCerts` allocates a fresh slice per call; two concurrent
  /status calls cannot alias backing memory.
- Missing or malformed cert files never take the endpoint offline.
- `/status` is GET-only; other methods return 405.
- Empty-slice JSON semantics preserved (`"relay_certs": []`, never
  `null`) for consumer simplicity.

## Deferred Items

- `"status": "degraded"` is not emitted today. Scope says `"ok"`
  unconditionally; a future PR can downgrade based on readiness probes.
- Agent cert expiry is deferred to B4 per the plan ("cert expiry from
  agents is additive; ship without it").
- No cert parse caching. Re-evaluate only if profiling shows it matters.

## Constraints Check

- [x] No emoji
- [x] No attribution footer
- [x] Files < 800 lines (admin.go at 717; new file 136)
- [x] Functions < 50 lines
- [x] `log/slog` only
- [x] Context propagation through `r.Context()` → `ListConnectedAgents`
- [x] Immutability — per-call slice allocation, defensive `CertPaths` copy
- [x] British → US (no British spellings introduced)
- [x] No hardcoded secrets
- [x] Conventional commit format

## Blockers

None. Ready for Step 5 (POST /reload, GET /config) once Step 3 and Step 4
land on main in order.
