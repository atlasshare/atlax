# Step 2 Report: Per-Source-IP Rate Limiting

**Date:** 2026-04-01
**Branch:** `phase4/rate-limiting`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Added token bucket rate limiting per source IP on client connections. When a client connects, the `ClientListener` checks whether the source IP has capacity. If not, the connection is closed immediately.

**IPRateLimiter:** per-IP `golang.org/x/time/rate.Limiter` with configurable rps and burst. Background goroutine sweeps stale entries (IPs not seen for >10 minutes).

**ClientListener integration:** checks `rateLimiter.Allow(ip)` before routing. Rate limiter is optional (nil = no limiting).

**API change:** `NewClientListener` now takes `ClientListenerConfig` struct instead of positional args (accommodates optional rate limiter).

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| errcheck on net.SplitHostPort | Unchecked error return | Handle error: fall back to full RemoteAddr string |
| x/time was indirect in go.mod | Prereq added it but go.mod marked indirect | `go get` made it direct |

## Decisions Made

1. **Non-blocking Allow()** -- Uses `rate.Limiter.Allow()` (returns bool immediately), not `Wait()` (blocks). Rejected connections are closed instantly.
2. **Stale entry cleanup** -- Background goroutine sweeps every 1 minute, removes IPs not seen for 10 minutes. Prevents memory growth from scanning.
3. **Single rate limiter shared across ports** -- All customer ports share one IPRateLimiter. Per-port limiters can be added later if needed.
4. **ClientListenerConfig struct** -- Changed from positional `(router, logger)` to config struct to cleanly add optional fields.

## Coverage Report

7 new rate limiter tests: allow under limit, reject over limit, independent IPs, len tracking, refill over time, sweep stale, sweep keeps fresh.
