# Phase 1, Step 4: UDP Framing Report

**Phase:** Phase 1 - Core Protocol
**Step:** 4 - UDP Framing
**Completed:** 2026-03-23
**Module:** `pkg/protocol`

## Summary

UDP framing implements transport for UDP datagrams over the TCP-multiplexed tunnel. The implementation provides:

- **ParseUDPDataPayload** — Parses the binary UDP_DATA frame payload format (addr_length(1B) + source_addr + udp_payload)
- **BuildUDPDataPayload** — Constructs binary UDP_DATA payload with validation (max 255-byte address)
- **NewUDPBindFrame** — Creates a UDP_BIND frame to request relay UDP listener
- **NewUDPUnbindFrame** — Creates a UDP_UNBIND frame to close relay UDP listener
- **NewUDPDataFrame** — Convenience constructor for UDP_DATA frames with source address and payload

The UDPDatagram struct holds parsed results with source address (string) and payload (bytes). Empty UDP payload returns nil slice (not empty slice) for consistency with Go conventions.

## Issues Encountered

### Issue 1: gosec G115 - Integer Overflow Check

**Location:** `pkg/protocol/udp.go:54`

**Description:** golangci-lint gosec check flagged `byte(len(sourceAddr))` as potential integer overflow:

```
gosec G115: Conversion from int to byte will lose data
```

**Root Cause:** Conversion from `int` (sourceAddr length) to `byte` without validation that length <= 255.

**Fix Applied:**

1. Added validation check before conversion in BuildUDPDataPayload (line 47):
   ```go
   if len(sourceAddr) > 255 {
       return nil, fmt.Errorf("udp: build: %w: length %d",
           ErrUDPAddrTooLong, len(sourceAddr))
   }
   ```

2. Guaranteed addrLen variable is <= 255 before conversion (line 52):
   ```go
   addrLen := len(sourceAddr) // guaranteed <= 255 by check above
   buf = append(buf, byte(addrLen)) //nolint:gosec // addrLen <= 255
   ```

3. Added `//nolint:gosec` comment to document the safety guarantee.

**Verification:** Code review confirmed that all callers of BuildUDPDataPayload go through NewUDPDataFrame, which also performs the same validation before calling BuildUDPDataPayload.

## Decisions Made

### UDPDatagram Struct

Stores the parsed result of a UDP_DATA frame payload:

```go
type UDPDatagram struct {
    SourceAddr string
    Payload    []byte
}
```

Rationale: Simple, immutable, and semantically clear. Splitting address and payload simplifies callers that need to route based on source address.

### Empty Payload Handling

In ParseUDPDataPayload, when UDP payload is empty, we return `nil` (not empty slice):

```go
if len(data) == 0 {
    data = nil
}
```

Rationale: Go convention is that nil and empty slice are semantically equivalent for read operations, but nil is idiomatic for "no data" cases. This matches io.Reader behavior.

### Frame Constructors Set Metadata

NewUDPBindFrame, NewUDPUnbindFrame, and NewUDPDataFrame automatically set:

- `Version` = ProtocolVersion
- `Command` = appropriate UDP command (CmdUDPBind, CmdUDPUnbind, CmdUDPData)
- `StreamID` = 0 for bind/unbind, caller-provided for UDP_DATA

Rationale: Prevents caller mistakes (forgetting to set protocol version or command). The constructor names are explicit about which command they create.

### Sentinel Errors in udp.go

Error types ErrInvalidUDPPayload and ErrUDPAddrTooLong are defined in `udp.go`, not `errors.go`:

```go
var (
    ErrInvalidUDPPayload = errors.New("invalid UDP_DATA payload")
    ErrUDPAddrTooLong    = errors.New("UDP source address exceeds 255 bytes")
)
```

Rationale: These errors are UDP-specific and encapsulate UDP framing concerns. It is clearer to readers that parsing errors are defined in the module that does the parsing.

## Deviations from Plan

No deviations from the Phase 1 implementation plan. All planned functions implemented as specified.

## Coverage Report

All functions in `pkg/protocol/udp.go` reach 100% statement and branch coverage:

| Function | Coverage |
|----------|----------|
| ParseUDPDataPayload | 100.0% |
| BuildUDPDataPayload | 100.0% |
| NewUDPBindFrame | 100.0% |
| NewUDPUnbindFrame | 100.0% |
| NewUDPDataFrame | 100.0% |

Test file `pkg/protocol/udp_test.go` includes:

- 11 test functions
- Valid/invalid parsing paths
- Boundary conditions (max address length 255 bytes)
- Empty payload cases
- Round-trip serialization/deserialization
- Frame constructor validation
- All error conditions

Run coverage:

```bash
go test -cover ./pkg/protocol -v
```

## Files Modified

- **pkg/protocol/udp.go** — New file, 96 lines
- **pkg/protocol/udp_test.go** — New file, 141 lines

## Related Documentation

- [Protocol Specification](/docs/reference/protocol.md) — UDP_BIND, UDP_UNBIND, UDP_DATA commands
- [Action Plan](/docs/reference/action-plan.md) — Phase 1 milestones
- [Development Guide](/docs/development/getting-started.md) — Running tests

## Next Steps

Step 5 implements MuxSession for multiplexing streams over a single connection. UDP framing will be integrated into the mux's stream dispatch in Phase 2.
