# Phase 3 Step 6 (B4) — CmdServiceList frame + relay caching + agent emission

**Branch:** `feat/protocol-service-list`
**Base commit:** `4bc1b16` (origin/main)
**Scope:** plan sub-steps 6a-6g EXCLUDING the `handleStatus` update on
line 396 of `plans/phase3-runtime-operator-plan.md` (which belongs to
parallel Step 4 / F2 and is deferred — see Deferred below).

## Summary

Adds a new connection-level wire frame, `CmdServiceList (0x0E)`, that the
agent emits once immediately after the mTLS + mux handshake. The payload
is a newline-separated UTF-8 list of the service names the agent
forwards locally. The relay caches this list (along with the peer
certificate's `NotAfter` timestamp captured from the TLS connection) on
the `LiveConnection`, exposes both via `AgentInfo`, and surfaces them in
`GET /agents` and `GET /agents/{id}` admin responses.

Byte `0x0E` was chosen per plan correction: `0x0C` and `0x0D` are
reserved for enterprise self-update (`CmdUpdateManifest`,
`CmdUpdateBinary`) and remain in the shared protocol enum so community
and enterprise builds agree on byte assignments.

## Sub-step deliverables

### 6a — `pkg/protocol/frame.go`

- Added `CmdServiceList Command = 0x0E`.
- Registered `"SERVICE_LIST"` in `commandNames`.
- Added reserved-byte comment explaining that `0x0C` and `0x0D` are
  enterprise-only.

### 6b — `pkg/relay/connection.go`

- Added `servMu sync.RWMutex` (kept separate from the existing `mu`
  guarding `lastSeen` to decouple heartbeat-rate writes from admin-rate
  reads of the service list).
- Added fields `services []string`, `certNotAfter time.Time`.
- Added methods:
  - `SetServices([]string)` — write-locked; stores a defensive copy.
  - `Services() []string` — read-locked; returns a defensive copy.
  - `SetCertNotAfter(time.Time)` — write-locked (single write per
    connection lifetime).
  - `CertNotAfter() time.Time` — read-locked.

### 6c — `pkg/protocol/mux_session.go`

- Added `serviceListCh chan []string` (buffer 1) on `MuxSession`; init
  in the constructor.
- Added dispatch case `CmdServiceList: m.handleServiceList(f)` to
  `handleFrame`.
- Implemented `handleServiceList(f)`: splits payload on `\n`, caps at
  `MaxServiceListCount = 1024`, filters empty tokens, non-blocking send
  to `serviceListCh` (drops silently if the buffer is full).
- Exported `ServiceListCh() <-chan []string` accessor.
- Implemented `SendServiceList(services []string) error`: builds the
  newline-joined payload, enforces `MaxPayloadSize`, enqueues at
  `PriorityControl`.

### 6d — `pkg/relay/listener.go` (`handleConnection`)

- Captured `state.PeerCertificates[0].NotAfter` into the
  `LiveConnection` right after mux creation.
- Waited for `CmdServiceList` with `serviceListWaitTimeout = 50ms`
  (named constant, documented with plan Risk Area 3 rationale).
- Registration proceeds regardless of whether the agent emits the
  frame; older agents incur only the 50ms penalty.

### 6e — `pkg/agent/client.go` + `pkg/agent/client_impl.go`

- Added `Services []string` to `ClientConfig`.
- `TunnelClient.Connect`: after mux creation, if `len(Services) > 0`,
  calls `mux.SendServiceList`; logs a warning on failure.
- Empty `Services` slice skips the send (no empty-payload frame on the
  wire).

### 6e — `cmd/agent/main.go`

- Derives `serviceNames` from `cfg.Services[*].Name`; skips empty
  names; plumbs into `ClientConfig.Services`.

### 6f — `pkg/relay/registry.go` + `pkg/relay/registry_impl.go`

- Added `Services []string` and `CertNotAfter time.Time` (with
  `json` tags) to `AgentInfo`; also added `json` tags to existing
  fields for consistency.
- `MemoryRegistry.ListConnectedAgents` now populates both from the
  `LiveConnection`.
- `pkg/relay/server_impl.go`: converted range-copy loops to indexed
  loops to absorb the increased `AgentInfo` size and silence gocritic.

### 6g — `pkg/relay/admin.go`

- Added `Services []string` and `CertNotAfter string` (RFC3339) to
  `AgentResponse`.
- Extracted shared mapping into `agentInfoToResponse(*AgentInfo)
  AgentResponse` (pointer receiver to avoid range-copy lint).
- Updated `handleAgents` and `getAgent` to use the helper.
- Converted `handleHealth` and `handleStats` range-copy loops to
  indexed iteration.

### Docs

- `docs/protocol/wire-format.md`: added rows for `0x0C`, `0x0D`, `0x0E`
  in the command table; added a "SERVICE_LIST payload format" section
  and a "Reserved bytes" section. Adjusted the "reserved for future
  extensions" line to `0x0F+`.

## Tests added

### `pkg/protocol/`

- `TestCmdServiceList_EncodeDecodeRoundTrip` — frame codec round trip
  across `Empty`, `SingleService`, `MultipleServices`, `UTF8Service`.
- Updated `TestCommandString` to include SERVICE_LIST.
- Updated `TestCommandIsValid` bounds to the new max (`CmdServiceList`)
  and to assert `Command(0x0F)` is not valid (previously `0x0E`).
- Added `CmdServiceList` seeds to `FuzzReadFrame` corpus, including one
  with a newline-separated payload.
- `TestMuxSession_ServiceListFrame` — happy-path delivery on
  `ServiceListCh`.
- `TestMuxSession_ServiceListFrame_FiltersEmpty` — empty tokens
  from `\n\n` are filtered.
- `TestMuxSession_ServiceListFrame_EmptyPayload` — empty payload
  delivers empty slice.
- `TestMuxSession_ServiceListFrame_NonBlocking` — second send drops
  silently; handler never blocks.
- `TestMuxSession_SendServiceList_EnqueuesControlPriority` — end-to-end
  through a real mux pair with a pre-filled control queue.
- `TestMuxSession_SendServiceList_FramePayloadFormat` — verifies
  `CmdServiceList`, `StreamID=0`, newline-joined payload.
- `TestMuxSession_SendServiceList_Empty` — explicit-empty slice still
  produces a zero-length frame (caller in agent is responsible for
  gating).
- `TestMuxSession_ServiceListFrame_DroppedOnClose` — no panic when mux
  is closed before send.

### `pkg/relay/`

- New file `pkg/relay/connection_test.go`:
  - `TestLiveConnection_ServicesAndCertExpiry`.
  - `TestLiveConnection_Services_DefensiveCopy` (input + output).
  - `TestLiveConnection_Services_Concurrent` (RWMutex exercise under
    `-race`).
  - `TestLiveConnection_SetServices_NilInput`.
  - `TestLiveConnection_UpdateLastSeen_StillWorksAfterServices`
    (regression for the split-lock refactor).
- `TestMemoryRegistry_ListConnectedAgents_IncludesServicesAndCert`.
- `TestAgentListener_ServiceListReceived` — end-to-end TLS mTLS
  handshake + mux + CmdServiceList through `AgentListener`; asserts
  `Services` and non-zero `CertNotAfter` on the registered agent.
- `TestAgentListener_NoServiceList_DoesNotBlock` — dials without
  sending the frame; asserts registration completes within 500ms
  (far inside the 50ms + buffer envelope) and `Services` is empty.
- `TestAdmin_AgentListIncludesServicesAndCert`.
- `TestAdmin_GetAgent_IncludesServicesAndCert`.
- `TestAdmin_AgentListOmitsCertWhenZero` — zero-value `CertNotAfter`
  still serializes to a parseable RFC3339 string.

### `pkg/agent/`

- `TestClient_Connect_SendsServiceList` — relay side of a pipe pair
  observes the emitted frame on `ServiceListCh` after `Connect`.
- `TestClient_Connect_SkipsEmptyServiceList` — relay never sees a
  frame when `ClientConfig.Services` is nil.

## Verification gates

| Gate | Status | Notes |
|------|--------|-------|
| `go vet ./...` | PASS | No output |
| `make lint` | PASS | 0 issues (several gocritic `rangeValCopy`/`hugeParam` findings introduced by the larger `AgentInfo`/`ClientConfig` were resolved via indexed iteration and a justified `nolint:gocritic` on `NewClient`) |
| `go build ./...` | PASS | |
| `go test -race ./...` | PASS | All packages green; `pkg/relay` executed full integration tests after `make certs-dev` populated `./certs/` |

## Coverage per package

| Package | Before (origin/main per memory) | After | Delta |
|---------|---------------------------------|-------|-------|
| `pkg/audit` | 96.3% | 96.3% | — |
| `pkg/auth` | 94.3% | 95.4% | +1.1 |
| `pkg/config` | 92.4% | 92.4% | — |
| `pkg/protocol` | 91.8% | 91.9% | +0.1 |
| `pkg/relay` | 88.7% (community reference) | 86.6% | -2.1 — new lines in `listener.go`, `connection.go`, `admin.go`; the new integration tests exercise the happy path but gocov counts untaken error branches. Still above 80% gate. |
| `pkg/agent` | 81.0% | 80.8% | -0.2 — `Connect` now has an extra error branch (SendServiceList warn-only) that no test exercises because the unit tests always succeed. Above 80% gate by 0.8pp. |

Gates in `plans/phase3-runtime-operator-plan.md` Definition of Done:
`pkg/relay` >= 80% (met at 86.6%). `pkg/protocol` stays high (91.9%).

## Security considerations addressed

1. **Frame payload DoS** — `MaxPayloadSize` (16 MB) still bounds the
   frame at the codec layer. On top of that, `handleServiceList` caps
   the parsed token count at `MaxServiceListCount = 1024` and emits a
   warning when the cap is exceeded. `strings.Split` on untrusted
   input is therefore bounded in CPU and allocation, independent of
   payload size.
2. **Send-side payload bounds** — `SendServiceList` rejects payloads
   that would exceed `MaxPayloadSize` with a wrapped `ErrInvalidFrame`.
3. **Timing side-channel** — The 50 ms `serviceListWaitTimeout` is a
   constant. Nothing in the logic branches on peer-supplied content
   before it expires, so no information leaks about the presence,
   size, or identity of advertised services.
4. **Tenant isolation** — Services and `CertNotAfter` are stored on
   the `LiveConnection` and therefore keyed by customer identity via
   the registry. There is no cross-tenant read path.
5. **Non-blocking handler** — `handleServiceList` does a non-blocking
   send to `serviceListCh`. A malicious or buggy peer emitting many
   `CmdServiceList` frames cannot wedge the mux read loop.
6. **Defensive copies** — `SetServices` stores a copy; `Services`
   returns a copy. Callers cannot corrupt registry state by mutating
   their input or the returned slice.
7. **No secrets on the wire** — The payload is strictly service
   *names* from the agent config, which are already known to the
   operator and are not sensitive. They are already logged by the
   relay in other contexts.

## Self-review findings (CRITICAL/HIGH/MEDIUM)

CRITICAL: none.

HIGH: none.

MEDIUM:
- `SendServiceList` is invoked before the agent records `c.mux` and
  `c.conn` in the `TunnelClient`. If the write fails synchronously (it
  will not, because the queue always accepts), the warn-only log is
  the only observable effect. This is intentional: we do not want the
  service-list emission to tear down an otherwise-healthy connection.
- `Connect` logs the send failure but does not retry. Acceptable:
  `CmdServiceList` is advisory metadata; a missed frame simply causes
  the admin API to show empty services until the next reconnect.

LOW / style:
- The separate `mu` and `servMu` locks in `LiveConnection` trade a
  small amount of footprint for isolation between heartbeat writes
  (high frequency) and admin-side reads of services/cert (low
  frequency, potentially long-held). Kept deliberately.

## Deferred follow-ups

1. **`GET /status` per-agent cert exposure** — The plan at line 396
   calls for `handleStatus` to gain an `agent_certs []CertExpiry`
   field. That handler does not exist on `main` at `4bc1b16`; it is
   introduced by Step 4 (F2) on a parallel branch
   (`feat/status-and-reload` per the work-order table). Once F2
   merges, a follow-up commit should extend its `handleStatus`
   response to include per-agent `CertNotAfter` sourced from the
   already-populated `AgentInfo.CertNotAfter`. No blocker; the data
   is already available on the wire and in the registry.

2. **Enterprise port** — When enterprise picks up this change via
   the manual upstream-sync workflow documented in
   `/Users/rubenyomenou/projects/atlax-department/CLAUDE.md`, the
   same diff applies verbatim to the forked `pkg/` tree. No
   interface changes, so no ADR required.

## Blockers

None.

## Commits

Not yet committed. Staged for a single commit on branch
`feat/protocol-service-list`. Do NOT push; user merges.
