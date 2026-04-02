# Step 1 Report: Hardened Systemd + Production Docker

**Date:** 2026-04-02
**Branch:** `phase6/infra`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Updated deployment files and wrote documentation for systemd and Docker.

**Systemd:** Fixed flag syntax (`-config` not `--config`), changed `Restart=on-failure` to `Restart=always`, added `ReadOnlyPaths=/etc/atlax` for cert protection. Full systemd guide with installation, env overrides, logging, and security hardening explanation.

**Docker:** Switched runtime from alpine to `gcr.io/distroless/static-debian12:nonroot`. Removed HEALTHCHECK (wget doesn't exist in distroless). Copied CA certs from build stage. Added Docker guide with run commands, compose example, and security notes.

## Decisions Made

1. **Distroless over alpine** -- No shell means no exec-into-container attacks. ~10MB smaller. Trade-off: no HEALTHCHECK in Dockerfile (use external monitoring).
2. **Restart=always** -- Agent reconnects automatically (Phase 5); relay should also restart on any exit, not just failure.
3. **ReadOnlyPaths=/etc/atlax** -- Prevents the process from modifying its own config or certs even if compromised.
