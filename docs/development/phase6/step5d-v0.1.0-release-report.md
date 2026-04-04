# Step 5d Report: v0.1.0 Release

**Date:** 2026-04-03
**Tag:** v0.1.0
**GitHub Release:** https://github.com/atlasshare/atlax/releases/tag/v0.1.0
**Status:** COMPLETED

---

## Summary

Tagged and released v0.1.0, the first versioned release of the atlax community edition. This is a pre-stable release (v0.x = no interface stability promise).

## Release Contents

Phases 1-6 Step 5: wire protocol, stream mux, flow control, mTLS auth, tunnel agent with reconnection, relay with multi-tenant isolation, rate limiting, Prometheus metrics, admin API, hardened systemd/Docker, fuzz testing.

## Stats at Release

- 270+ tests
- 88% relay package coverage, 86%+ overall
- 44+ source files, ~11,500 lines Go
- Fuzz: 895K executions, 0 crashes

## Versioning Strategy

SemVer with v0.x pre-stable contract:
- v0.x = breaking changes expected, enterprise pins exact versions
- v1.0.0 = interfaces frozen, enterprise API contract locked
- Patches unbounded (v0.1.15 is valid)

## Subsequent Patches

- v0.1.1: moved `internal/audit` and `internal/config` to `pkg/` for enterprise module access (PR #77)
- v0.1.2: added `StartWithListener` methods for enterprise fd passing (PR #81)
