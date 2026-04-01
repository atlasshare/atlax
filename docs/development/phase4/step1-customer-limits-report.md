# Step 1 Report: Per-Customer Stream and Connection Limits

**Date:** 2026-03-31
**Branch:** `phase4/customer-limits`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Enforced two resource limits that existed in config but were not checked at runtime:

**Per-customer stream limits:** `PortRouter.Route` checks `mux.NumStreams()` against `maxStreams` before opening a new stream. If exceeded, returns `ErrStreamLimitExceeded` and closes the client connection.

**Per-customer connection limits:** `MemoryRegistry` gains `SetCustomerLimit(customerID, maxConns)` and `customerLimits` map. Default is 1 (replace-on-reconnect, existing behavior). Configurable per customer via `CustomerConfig.MaxConnections`.

**Config:** `CustomerConfig` gains `MaxConnections` field. `PortIndexEntry` carries `MaxStreams` through to `PortRouter`.

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| `AddPortMapping` signature change cascaded | New `maxStreams` param required updating interface, server, and all tests | Updated all callers; existing tests pass 0 (unlimited) |
| gocritic: shadowing `max` builtin | Parameter named `max` | Renamed to `maxConns` |

## Decisions Made

1. **Stream limit checked BEFORE OpenStreamWithPayload** -- Reject early, no wasted mux resources.
2. **maxStreams=0 means unlimited** -- Backward compatible; existing configs without the field work unchanged.
3. **Connection limit default is 1** -- Preserves existing replace-with-GOAWAY behavior. Multi-agent (limit > 1) deferred since the map only holds one connection per customer ID.
4. **Limits carried through PortIndexEntry** -- BuildPortIndex propagates MaxStreams from CustomerConfig into the port index so the router has the limit at routing time.

## Coverage Report

2 new tests: StreamLimitEnforced, StreamLimitZeroIsUnlimited.
All existing tests pass with `maxStreams: 0` (unlimited).
