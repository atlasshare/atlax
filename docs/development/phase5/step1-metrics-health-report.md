# Step 1 Report: Metrics Wiring + Health Check + Rate Limit Config

**Date:** 2026-04-02
**Branch:** `phase5/metrics-health`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Three deliverables:

**Metrics wiring:** The Phase 4 Metrics struct is now called from production code:
- PortRouter.Route: StreamOpened/StreamClosed on successful routing, ClientRejected("stream_limit") on limit exceeded
- MemoryRegistry.Register/Unregister: ConnectionRegistered/ConnectionUnregistered
- ClientListener.handleClient: ClientRejected("rate_limited") on rate limit rejection

**Health check endpoint:** AdminServer serves `/healthz` (JSON: status, agent count, stream count) and `/metrics` (Prometheus format) on the admin port.

**Rate limit config:** RateLimitConfig struct (RequestsPerSecond, Burst) added to CustomerConfig. Per-customer rate limiting is configurable in YAML.

## Decisions Made

1. **SetMetrics pattern** -- Optional `*Metrics` field on PortRouter and MemoryRegistry, set via SetMetrics(). Nil check before every call. No metrics dependency in constructor.
2. **Lookup customer before rate limit** -- ClientListener.handleClient now looks up the customer ID first, then rate limits. This allows the ClientRejected metric to carry the customer_id label.
3. **AdminServer as separate type** -- Not part of Relay. Started independently in cmd/relay. Clean separation: relay manages tunnels, admin manages observability.
4. **ReadHeaderTimeout on admin server** -- 10 seconds, prevents Slowloris attacks (gosec G112).

## Coverage Report

2 new admin tests (healthz, metrics endpoint). All existing tests pass.
