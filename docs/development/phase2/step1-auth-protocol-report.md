# Step 1 Report: Protocol Completions + mTLS Authentication

**Date:** 2026-03-29
**Branch:** `phase2/auth-protocol`
**PR:** #18
**Status:** COMPLETED

---

## Summary

Step 1 delivered two halves: (A) completing three Phase 1 protocol gaps that blocked real traffic, and (B) implementing mTLS authentication for both relay and agent sides.

**Half A -- Protocol completions in `pkg/protocol/`:**
- STREAM_OPEN handshake: OpenStream sends STREAM_OPEN and blocks until peer sends ACK via pendingOpen map
- Write draining: MuxSession.drainStream goroutine reads from StreamSession.writeOut channel and emits STREAM_DATA frames through the write queue
- WINDOW_UPDATE wiring: handleWindowUpdate calls FlowWindow.Update() on the correct stream or connection (stream ID 0)

**Half B -- mTLS authentication in `pkg/auth/`:**
- Configurator builds tls.Config for relay (RequireAndVerifyClientCert) and agent (RootCAs + ServerName)
- FileStore loads PEM cert/key pairs, CA pools, and polls for cert rotation
- ExtractIdentity parses customer-{uuid} or relay.* CN from peer cert with SHA-256 fingerprint

## Issues Encountered

| Issue | Root Cause | Fix |
|-------|-----------|-----|
| CI test failure: `open ../../certs/agent.crt: no such file or directory` | Dev certificates are gitignored; CI had no `certs/` directory | Added `make certs-dev` step to CI workflow before test execution |
| CertificateStore.LoadCertificate scaffold had single `path` parameter | Cannot load cert+key pair with one path; need both certPath and keyPath | Changed interface signature to `LoadCertificate(certPath, keyPath string)` |
| `TestConfigurator_HandshakeFailsWrongClientCA` passed on client side | TLS 1.3: server-side rejection is asynchronous; client handshake may succeed before seeing the alert | Changed test to assert at least one side fails (server or client) |
| gosec G402: TLS MinVersion too low | WithMinVersion option allows overriding the default TLS 1.3 minimum | Added `//nolint:gosec` with explanation that default is TLS 1.3, option is for testing |
| gocritic hugeParam on TLSPaths (80 bytes) | TLSPaths has 5 string fields | Added `//nolint:gocritic` -- value semantics preferred for small config struct |

## Decisions Made

1. **StreamSession.writeOut is a buffered channel (cap 64)** -- Replaces the `[][]byte` writeBuf slice from Phase 1. Enables MuxSession drain goroutine to consume writes without polling. Channel buffer of 64 prevents blocking on small bursts.

2. **OpenStream blocks on per-stream ackCh** -- Each OpenStream call registers a `chan struct{}` in `pendingOpen[streamID]`. handleStreamOpen with ACK flag closes the channel. Context cancellation cleans up.

3. **CertificateStore.LoadCertificate takes two paths** -- Scaffold had `LoadCertificate(path string)` but Go's `tls.LoadX509KeyPair` requires both cert and key paths. Changed interface signature. Precedent: Phase 1 changed HalfClosed enum from scaffold.

4. **FileStore uses polling (not fsnotify) for cert rotation** -- Cross-platform reliability. fsnotify has known issues on network filesystems and Docker volumes. Configurable poll interval (default 24h).

5. **closedCh added to StreamSession** -- Closed exactly once via sync.Once when stream reaches Closed or Reset state. Used by drainStream goroutine and Write to detect stream termination.

## Deviations from Plan

- **CertificateStore interface changed** -- LoadCertificate now takes `(certPath, keyPath string)` instead of `(path string)`. Required for tls.LoadX509KeyPair.
- **CI workflow updated** -- Added `make certs-dev` step. Not in original plan but required for CI to pass.
- **No WatchForRotation integration test with actual cert rotation** -- Test verifies reload callback is called when file content changes (via appending newline). Full rotation with CSR/sign cycle deferred to Phase 5.

## Coverage Report

```
pkg/auth    94.3% of statements
pkg/protocol  92.4% of statements
```

Per-function for auth:
- certs_impl.go: LoadCertificate 100%, LoadCertificateAuthority 100%, WatchForRotation 81%
- identity.go: ExtractIdentity 91.7%
- mtls.go: ServerTLSConfig 100%, ClientTLSConfig 100%
