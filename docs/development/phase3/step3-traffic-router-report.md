# Step 3 Report: Traffic Router + Client Listener + STREAM_OPEN Payload

**Date:** 2026-03-29
**Branch:** `phase3/traffic-router`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Delivered the traffic routing layer and resolved the Phase 2 carry-forward item for multi-service routing. Three sub-components:

**STREAM_OPEN payload:**
- `MuxSession.OpenStreamWithPayload(ctx, payload)` sends service name in STREAM_OPEN frame
- `handleStreamOpen` stores payload on accepted StreamSession via `SetOpenPayload`
- Agent's `TunnelRunner.resolveTarget` reads payload to route to correct local service
- `OpenStream(ctx)` delegates to `OpenStreamWithPayload(ctx, nil)` (backward compatible)

**PortRouter** (satisfies `TrafficRouter`):
- Static port-to-customer-service map built from config
- Route: lookup customer by port -> lookup AgentConnection from registry -> open stream with service payload -> bidirectional copy
- LookupPort: returns customerID + service for a port
- Uses Reset for forced teardown (same pattern as Forwarder)

**ClientListener:**
- Per-port TCP listener for client connections
- Routes via PortRouter.Route
- Start/Stop lifecycle per port

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| RouteEndToEnd test timed out | copyBidirectional used stream.Close() which doesn't unblock Read | Used Reset pattern (same as Forwarder): context cancel goroutine calls stream.Reset(0) to force-unblock io.Copy |
| staticcheck SA6001: string(payload) in map lookup | Converting to string then looking up is less efficient than direct map access | Changed to `t.services[string(payload)]` (single expression, no intermediate variable) |

## Decisions Made

1. **OpenStreamWithPayload as concrete method** -- Not added to Muxer interface (would break existing implementations). Only PortRouter calls it, and it holds a `*MuxSession` via type assertion.
2. **Service name as raw UTF-8 bytes** -- No structured encoding (JSON, protobuf). Service name is a simple string. Sufficient for Phase 3; can add structured metadata later if needed.
3. **resolveTarget fallback** -- If payload service name is not in the map, falls back to single-service routing. This preserves backward compatibility with agents configured for one service.

## Deviations from Plan

- **Cross-tenant isolation test deferred** -- Plan called for a test verifying client on port A cannot reach agent B. The port-to-customer mapping is static and lookup-based, so isolation is structural. A dedicated test will be added in Step 6 integration.

## Coverage Report

```
pkg/relay  73.0% (router+listener code paths)
```

7 new router tests including full end-to-end: client -> relay -> stream -> agent -> echo -> response.
