# Step 5a Report: Admin API Port Lifecycle Fix

**Date:** 2026-04-03
**Branch:** `phase6/port-lifecycle`
**PR:** #76
**Status:** COMPLETED

---

## Summary

Fixed the admin API port lifecycle gap from Step 4: `POST /ports` now starts a TCP listener in addition to adding the routing entry, and `DELETE /ports/{port}` now stops the listener. Added `StopPort` method to ClientListener and `ListenAddr` field to `PortCreateRequest`.

## Changes

- `pkg/relay/client_listener.go` -- Added `StopPort(port int) error`
- `pkg/relay/admin.go` -- Added `ctx` field to AdminServer, wired `StartPort` into `createPort` (goroutine with stored context, rollback on bind failure), wired `StopPort` into `deletePort`
- `pkg/relay/admin.go` -- Added `ListenAddr` field to `PortCreateRequest` (default `0.0.0.0`)
- `pkg/relay/client_listener_test.go` -- New file: 5 tests (StartPort, StopPort, Stop, Addr)
- `pkg/relay/admin_test.go` -- Updated helper to return `*ClientListener`, 4 new tests (create starts listener, delete stops listener, default listen addr, duplicate conflict)

## Design Decisions

1. **Goroutine with stored context** -- `StartPort` blocks in an accept loop, so `createPort` launches it in a goroutine derived from `a.ctx` (set in `AdminServer.Start`), not the HTTP request context.
2. **50ms readiness check** -- After launching `StartPort`, brief wait + select on error channel detects immediate bind failures (port in use, permission denied). On failure, rolls back `RemovePortMapping`.
3. **StopPort warning on config-started ports** -- `deletePort` logs a warning if `StopPort` fails (port may have been started from config at boot, not admin API). HTTP response still succeeds since the mapping was removed.

## Coverage

`pkg/relay`: 72.0% -> 78.4% (9 new tests)
