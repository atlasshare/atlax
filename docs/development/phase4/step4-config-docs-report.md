# Step 4 Report: Per-Port ListenAddr + Config + Documentation

**Date:** 2026-04-01
**Branch:** `phase4/config-docs`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Three deliverables:

**Per-port ListenAddr:** `PortConfig.ListenAddr` field (default `0.0.0.0`). Set to `127.0.0.1` for reverse proxy deployments. Carried through `PortIndexEntry` to `Relay.Start` which uses it in the bind address.

**Config updates:** `relay.example.yaml` updated with `max_connections`, `max_streams`, and `listen_addr` per port with documentation comments.

**Multi-tenancy guide:** `docs/operations/multi-tenancy.md` covering isolation model, resource limits, Caddy reverse proxy pattern, metrics, and complete multi-tenant config example.

## Decisions Made

1. **Default 0.0.0.0** -- Backward compatible. Existing configs without `listen_addr` bind to all interfaces (same as before).
2. **Per-port, not global** -- Each port can have a different bind address. HTTP ports behind Caddy use 127.0.0.1, SMB ports exposed directly use 0.0.0.0.

## Coverage Report

2 new tests: ListenAddr default (0.0.0.0), ListenAddr custom (127.0.0.1).
