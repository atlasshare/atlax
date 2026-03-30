# Step 5 Report: cmd/relay + Graceful Shutdown

**Date:** 2026-03-29
**Branch:** `phase3/relay-binary`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Wired all relay components into the `cmd/relay/main.go` binary and implemented the Relay server facade with graceful shutdown.

**Relay** (satisfies `Server` interface):
- Orchestrates AgentListener, ClientListener, PortRouter, MemoryRegistry
- Start: registers port mappings from config, starts per-port client listeners, starts agent listener
- Stop: sends GOAWAY to all agents, stops client listeners, unregisters agents

**cmd/relay/main.go:**
- `run()` pattern (same as cmd/agent)
- Loads relay config, builds mTLS server config, creates all components
- Signal handling (SIGINT/SIGTERM) with configurable grace period

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| `relay.RelayServer` stutters | Go naming convention | Renamed to `Relay` with `ServerDeps` for the config struct |
| Unused `mu`/`addr` fields | Leftover from initial design | Removed |

## Decisions Made

1. **Relay (not RelayServer)** -- Avoids `relay.RelayServer` stutter. Interface is `Server`, concrete is `Relay`.
2. **ServerDeps (not Config)** -- Avoids confusion with `config.RelayConfig`. Named "Deps" because it holds pre-built components, not raw configuration values.
3. **Per-port goroutines for client listeners** -- Each customer port gets its own accept loop goroutine. Clean shutdown via context cancellation.

## Deviations from Plan

- None significant. Naming adjusted for linter compliance.

## Coverage Report

3 new server tests: StopWithoutStart, StartRegistersPortMappings, GracefulShutdownSendsGoAway.
