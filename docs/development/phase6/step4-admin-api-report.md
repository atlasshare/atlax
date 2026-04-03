# Step 4 Report: Admin API

**Date:** 2026-04-03
**Branch:** `phase6/admin-api`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Full CRUD admin API on unix domain socket + optional TCP. Both community and enterprise editions get the same endpoints.

**Endpoints:**
- `GET /healthz` -- health check (existed)
- `GET /metrics` -- Prometheus metrics (existed)
- `GET /ports` -- list port-to-customer mappings
- `POST /ports` -- add mapping at runtime (no restart)
- `DELETE /ports/{port}` -- remove mapping
- `GET /agents` -- list connected agents with metadata
- `DELETE /agents/{customerID}` -- disconnect agent (GOAWAY + unregister)
- `GET /stats` -- relay uptime, agent count, stream count

**Transport:**
- Unix socket: `/var/run/atlax.sock` (permissions 0660, local access only)
- TCP: `admin_addr` from config (optional, for remote access)
- Both can run simultaneously

**Persistence:** Ephemeral. Runtime changes lost on restart. Config file is source of truth.

## Design Decisions

1. **Unix socket for community, TCP for enterprise** -- Socket needs no auth (file permissions). TCP needs bearer token (future enterprise feature).
2. **Full CRUD for both editions** -- Gating write operations behind enterprise makes community feel broken. Enterprise moat is distributed infra (multi-relay, fleet management), not single-node CRUD.
3. **writeError helper** -- Consistent JSON error responses via helper function instead of inline fmt.Sprintf.
4. **AdminConfig struct** -- Replaces the old positional constructor. Carries router and client listener references for CRUD operations.
5. **ats integration (future)** -- The `ats` CLI tool will detect the socket and use it for operations instead of file editing.

## Coverage Report

11 tests: health, metrics, stats, list ports, create port, invalid JSON, missing fields, delete port, delete port not found, list agents, delete agent.
