# Step 2 Report: Monitoring Stack

**Date:** 2026-04-02
**Branch:** `phase6/monitoring`
**PR:** pending
**Status:** COMPLETED

---

## Summary

**Per-customer rate limiting from YAML config:** ClientListener now uses per-customer IPRateLimiters built from `CustomerConfig.RateLimit`. The old global `RateLimiter` field is replaced with a `rateLimiters map[string]*IPRateLimiter` and `SetRateLimiter(customerID, rps, burst)`. cmd/relay wires from config at startup.

**Prometheus guide:** `docs/operations/prometheus.md` -- scrape config, available metrics, useful PromQL queries, retention, health check.

**Grafana dashboard:** `deployments/grafana/relay-dashboard.json` -- 8 panels: active agents, active streams, rejection rate, stream totals, per-customer streams, open rate, connections, rejections by reason.

**Alerting rules:** `deployments/prometheus/alerts.yml` -- 5 rules: AgentDisconnected, AgentDown (flapping), HighRejectionRate, StreamLimitApproaching, RelayDown.

## Decisions Made

1. **Per-customer map, not global limiter** -- Each customer can have different rate limits. Customers without `rate_limit` in config have no limiting.
2. **SetRateLimiter called at startup** -- cmd/relay iterates customers and configures limiters before server start. Runtime changes require relay restart (or future admin API).
3. **PortIndexEntry carries RateLimit** -- Available during routing for future per-port rate limit granularity.
