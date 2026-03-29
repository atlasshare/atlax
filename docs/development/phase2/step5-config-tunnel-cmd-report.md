# Step 5 Report: Config Loader + Tunnel + cmd/agent

**Date:** 2026-03-29
**Branch:** `phase2/agent-binary`
**PR:** #22
**Status:** IN PROGRESS (awaiting merge)

---

## Summary

Step 5 wired all Phase 2 components into a working agent binary. Three sub-components:

**Config Loader** (`internal/config/loader.go`):
- FileLoader reads YAML via `gopkg.in/yaml.v3`, validates required fields, applies environment variable overrides
- YAML tags added to all config structs for correct unmarshaling
- Env overrides: ATLAX_RELAY_ADDR, ATLAX_TLS_CERT, ATLAX_TLS_KEY, ATLAX_TLS_CA, ATLAX_LOG_LEVEL

**Tunnel Orchestrator** (`pkg/agent/tunnel_impl.go`):
- TunnelRunner accepts streams from MuxSession via AcceptStream loop
- Routes streams to local services via Forwarder
- Single-service routing for Phase 2 (all streams to one target)
- Tracks active/total streams and uptime via atomic counters

**Agent Binary** (`cmd/agent/main.go`):
- `run()` pattern: main calls run(), only os.Exit on error return
- Full startup sequence: config -> logger -> audit -> mTLS -> client -> connect -> tunnel -> signal wait -> shutdown

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| gocritic exitAfterDefer: os.Exit after defer | Original main() had `defer emitter.Close()` then later `os.Exit(1)` | Refactored to `run()` pattern: all logic in run() which returns error, main() only does os.Exit |
| gofmt alignment on config struct fields | YAML tags changed field alignment | Ran gofmt -w |
| Config structs missing YAML tags | Scaffold config.go had no yaml tags, so YAML unmarshaling produced empty fields | Added `yaml:"field_name"` tags to all struct fields |
| ServiceMapping defined in both config and agent packages | Scaffold had duplicate types in internal/config and pkg/agent | cmd/agent/main.go maps between them explicitly (config.ServiceMapping -> agent.ServiceMapping) |

## Decisions Made

1. **run() pattern for main** -- All logic in `run() error`, main() only does `os.Exit(1)` on error. This ensures deferred cleanup (emitter.Close, client.Close) always runs. Satisfies gocritic exitAfterDefer.

2. **Environment variables override YAML values** -- Applied after YAML parsing, before validation. This allows deploying with a base YAML config and overriding specific values via env vars in containers/systemd.

3. **Single-service routing in Phase 2** -- TunnelRunner.resolveTarget returns the sole configured service address if exactly one service is configured. Multi-service routing (parsing STREAM_OPEN payload for service name) requires relay-side changes and is deferred to Phase 3.

4. **Validation on load, not at runtime** -- FileLoader.validateAgentConfig checks required fields immediately after parsing. No invalid config reaches the runtime. Fail fast with clear error messages.

5. **YAML tags match config file conventions** -- snake_case (e.g., `cert_file`, `keepalive_interval`) matching the `agent.example.yaml` format. Added fields to RelayConnection for reconnect/keepalive settings that were in the YAML example but missing from the Go struct.

## Deviations from Plan

- **RelayConnection struct expanded** -- Added ReconnectInterval, MaxReconnectBackoff, KeepaliveInterval, KeepaliveTimeout fields to match agent.example.yaml. These were implicitly used by ClientConfig but not loadable from YAML.
- **No integration test for cmd/agent** -- The binary is verified to build (`go build ./cmd/agent/...`) but not executed in a test. Full e2e test is Step 6.

## Coverage Report

```
internal/config  100.0% of statements
pkg/agent         87.0% of statements
```

Per-function for new code:
- loader.go: LoadAgentConfig 100%, LoadRelayConfig 100%, validateAgentConfig 100%, applyAgentEnvOverrides 100%
- tunnel_impl.go: Start 75.8%, Stop 70%, Stats 100%, resolveTarget 75%
- Tunnel lower coverage due to paths requiring relay (type assertion failure, multi-service routing empty return)

Config: 15 tests. Tunnel: 4 tests.
