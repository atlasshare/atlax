# atlax - Project Conventions

## Overview

atlax is a custom reverse TLS tunnel with TCP stream multiplexing in Go (Community Edition of the AtlasShare relay component). It enables customer nodes behind CGNAT to expose local services through a public relay.

## Module

- **Module path:** `github.com/atlasshare/atlax`
- **Go version:** 1.25 minimum
- **License:** Apache 2.0

## Architecture

Two binaries communicate over a single TLS connection using a custom wire protocol:

- **`atlax-relay`** — Runs on a public VPS. Accepts agent TLS connections, accepts client TCP connections on service ports, and routes traffic through multiplexed streams.
- **`atlax-agent`** — Runs on customer nodes. Dials out to the relay over mTLS, receives stream requests, and forwards traffic to local services (e.g., Samba, HTTP).

## Package Layout

```
cmd/relay/       Entry point for atlax-relay binary
cmd/agent/       Entry point for atlax-agent binary
pkg/protocol/    Wire protocol: frame encoding, stream multiplexing, flow control
pkg/relay/       Relay server: TLS listener, agent registry, traffic routing
pkg/agent/       Tunnel agent: connection management, local service forwarding
pkg/auth/        mTLS configuration, certificate management, identity extraction
internal/config/ Configuration loading from YAML + env var overrides
internal/audit/  Append-only audit event emission for lifecycle events
```

- `pkg/` — Public API, importable by other projects
- `internal/` — Private to this module, not importable externally
- `cmd/` — Binary entry points, minimal logic

## Wire Protocol

12-byte header per frame:

| Field | Size | Description |
|-------|------|-------------|
| Version | 1B | Protocol version (0x01) |
| Command | 1B | Frame type |
| Flags | 1B | Bitfield (FIN, ACK) |
| Reserved | 1B | Future use (0x00) |
| Stream ID | 4B | Big-endian stream identifier |
| Payload Length | 4B | Big-endian, max 16MB |

Commands: STREAM_OPEN (0x01), STREAM_DATA (0x02), STREAM_CLOSE (0x03), STREAM_RESET (0x04), PING (0x05), PONG (0x06), WINDOW_UPDATE (0x07), GOAWAY (0x08), UDP_BIND (0x09), UDP_DATA (0x0A), UDP_UNBIND (0x0B).

Stream IDs: relay-initiated = odd, agent-initiated = even, 0 = connection-level.

## Authentication

- **mTLS** with TLS 1.3 minimum
- **Certificate hierarchy:** Root CA -> Relay Intermediate CA -> relay cert; Root CA -> Customer Intermediate CA -> customer-{uuid} cert
- **Identity extraction:** CN=customer-{uuid} from peer certificate
- **Session resumption:** Enabled for performance
- **Cert validity:** 90-day rotation cycle
- Zero-trust: every connection authenticated, no plaintext fallback

## Community vs Enterprise Boundaries

Use interfaces at all extension points:

- **AgentRegistry** — Community: in-memory map. Enterprise: Redis/etcd.
- **Audit Emitter** — Community: structured log output. Enterprise: event bus/SIEM.

Enterprise implementations are injected via interface satisfaction, not build tags.

## Build Commands

```bash
make build       # Build both binaries
make test        # Run tests with -race flag
make lint        # Run golangci-lint
```

## Testing Conventions

- Table-driven tests
- Always use `-race` flag
- Test files alongside source: `foo_test.go` next to `foo.go`
- Use `testify` for assertions where helpful
- Minimum 90% coverage target

## Code Conventions

- **Logging:** `log/slog` structured logging (no fmt.Println in production code)
- **Context:** Propagate `context.Context` through all functions that do I/O
- **Immutability:** Prefer immutability by default. If mutating an object has clear performance or ergonomic benefits that outweigh the cost, flag it for human review before proceeding.
- **Error handling:** Wrap errors with `fmt.Errorf("operation: %w", err)` for context
- **Naming:** Use explicit, self-describing names. The name of an object should tell you exactly what it does, and it should not do anything else. Package names: lowercase single-word (protocol, relay, agent, auth, config, audit).
- **File size:** 200-400 lines typical, 800 max
- **Functions:** Under 50 lines

## Security Rules

- No plaintext connections — TLS 1.3 minimum everywhere
- No cross-tenant routing — relay must verify customer ID matches on every stream
- No hardcoded secrets, passwords, or private keys in source
- Parameterize all queries if database is used
- Audit all connection and stream lifecycle events

## Documentation Conventions

- One topic per file
- Strict separation of concerns between doc directories
- Architecture, protocol, security, operations, and development docs are separate

## Special Instructions

- **No co-author or generated-by lines** in commit messages. No attribution footers of any kind.
- **No emoji** anywhere in the codebase — code, comments, docs, commit messages, config files. Zero exceptions.

## Issue Reporting

When you discover a bug outside your current domain:
1. DO NOT fix it or change context
2. Run: `~/projects/dev-pipeline/scripts/report-issue.sh atlax --severity <critical|high|medium|low> --domain <frontend|backend|infra|data|devops> --title "Short description" --body "Details"`
3. Continue your current work
