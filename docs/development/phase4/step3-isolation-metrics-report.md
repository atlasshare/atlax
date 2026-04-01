# Step 3 Report: Cross-Tenant Isolation Test + Per-Customer Metrics

**Date:** 2026-04-01
**Branch:** `phase4/isolation-metrics`
**PR:** pending
**Status:** COMPLETED

---

## Summary

**Cross-tenant isolation test** (closes #42): Two customers, two agents, two port sets. Verified:
1. Client on port-A reaches agent-A (gets "A:" prefix response)
2. Client on port-B reaches agent-B (gets "B:" prefix response)
3. Agent-A disconnects: client on port-A gets "agent not found" error
4. Port-A mapped to customer-A but customer-A not registered: cannot reach customer-B's agent

**Prometheus metrics:** Counters and gauges per customer_id:
- `atlax_streams_total`, `atlax_streams_active`
- `atlax_connections_total`, `atlax_connections_active`
- `atlax_clients_rejected_total{customer_id, reason}`

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| Isolation test: clientA.Read got EOF | Agent echo goroutine called s.Close() immediately after Write, racing STREAM_CLOSE with STREAM_DATA in the mux | Removed Close from echo goroutine; let client-side close drive teardown |
| prometheus/client_golang was indirect | Prereq added it but go.sum didn't have all transitive deps | `go get` made it direct with full dependency resolution |

## Decisions Made

1. **In-stream echo, not TCP echo server** -- Isolation test uses in-mux echo goroutines (read from stream, write back with prefix) rather than real TCP servers. Faster, deterministic, no port allocation.
2. **Metrics not yet wired into router/registry** -- Metrics struct created and tested. Wiring into Route/Register happens in Step 4 or 5 when the relay binary is updated.
3. **200ms sleep for mux data flow** -- Data traverses client pipe -> relay mux -> agent mux -> echo -> agent mux -> relay mux -> client pipe. 200ms is generous but reliable in CI.

## Coverage Report

2 isolation tests + 4 metrics tests. All passing with -race.
