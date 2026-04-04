# Interface Stability Contract

This document defines which interfaces and types in the community module (`github.com/atlasshare/atlax`) form the enterprise API surface. The enterprise module (`github.com/atlasshare/atlax-enterprise`) depends on these contracts.

---

## Stable Interfaces

Enterprise implementations MUST satisfy these interfaces with compile-time checks (`var _ Interface = (*Impl)(nil)`).

### pkg/relay.AgentRegistry (5 methods)

```go
type AgentRegistry interface {
    Register(ctx context.Context, customerID string, conn AgentConnection) error
    Unregister(ctx context.Context, customerID string) error
    Lookup(ctx context.Context, customerID string) (AgentConnection, error)
    Heartbeat(ctx context.Context, customerID string) error
    ListConnectedAgents(ctx context.Context) ([]AgentInfo, error)
}
```

Community: `MemoryRegistry` (in-process `sync.RWMutex` map).
Enterprise: `RedisRegistry` (Redis hash with TTL, cross-relay lookup).

### pkg/relay.AgentConnection (6 methods)

```go
type AgentConnection interface {
    CustomerID() string
    Muxer() protocol.Muxer
    RemoteAddr() net.Addr
    ConnectedAt() time.Time
    LastSeen() time.Time
    Close() error
}
```

Community: `LiveConnection`.
Enterprise: same (wraps community `LiveConnection`).

### pkg/relay.TrafficRouter (3 methods)

```go
type TrafficRouter interface {
    Route(ctx context.Context, customerID string, clientConn net.Conn, port int) error
    AddPortMapping(customerID string, port int, service string, maxStreams int) error
    RemovePortMapping(customerID string, port int) error
}
```

Community: `PortRouter` (in-memory portMap).
Enterprise: same community implementation; enterprise moat is the registry, not the router.

### pkg/relay.Server (3 methods)

```go
type Server interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Addr() net.Addr
}
```

Community: `Relay`.
Enterprise: same (wraps community `Relay` with fd-passing support).

### pkg/auth.CertificateStore (3 methods)

```go
type CertificateStore interface {
    LoadCertificate(certPath, keyPath string) (tls.Certificate, error)
    LoadCertificateAuthority(path string) (*x509.CertPool, error)
    WatchForRotation(ctx context.Context, certPath, keyPath string, reload func(tls.Certificate)) error
}
```

Community: `FileStore` (PEM files on disk, poll-based rotation).
Enterprise: `VaultStore` (Vault PKI / step-ca, CSR submission, automated issuance).

### pkg/auth.TLSConfigurator (2 methods)

```go
type TLSConfigurator interface {
    ServerTLSConfig(opts ...TLSOption) (*tls.Config, error)
    ClientTLSConfig(opts ...TLSOption) (*tls.Config, error)
}
```

Community: `Configurator`.
Enterprise: same community implementation (delegates to `CertificateStore`).

### pkg/audit.Emitter (2 methods)

```go
type Emitter interface {
    Emit(ctx context.Context, event Event) error
    Close() error
}
```

Community: `SlogEmitter` (buffered async channel, drains to `log/slog`).
Enterprise: `SIEMEmitter` (Kafka/NATS event bus).

---

## Stable Types

These exported types are used by enterprise code and must not have fields removed or renamed.

### pkg/relay

- `AgentInfo` -- metadata for listing/monitoring
- `PortAllocation` -- port assignment tracking
- `TrafficRouterConfig` -- port range configuration

### pkg/auth

- `CertRotationConfig` -- rotation watcher settings
- `CertInfo` -- certificate metadata
- `Identity` -- extracted mTLS identity (CustomerID, RelayID, CertFingerprint)
- `TLSPaths` -- file paths for cert, key, CA
- `TLSOption` -- functional options for TLS config

### pkg/audit

- `Event` -- immutable audit event record
- `Action` -- event type constants (all `Action*` constants)

### pkg/config

- `RelayConfig`, `AgentConfig` -- top-level config structures
- `CustomerConfig`, `PortConfig` -- customer/port configuration
- `UpdateConfig` -- agent self-update settings
- `LogConfig`, `MetricsConfig` -- observability configuration
- `RateLimitConfig` -- per-customer rate limiting
- `TLSPaths` -- TLS file path configuration

---

## Stability Rules

1. **Adding methods to an existing interface is a breaking change.** It requires all implementations (including enterprise) to be updated simultaneously.

2. **New extension points use new interfaces.** If a new capability is needed, define a new interface rather than extending an existing one.

3. **Enterprise CI validates the contract.** The enterprise repo's `go build` imports community interfaces. Any breaking change fails the enterprise build immediately.

4. **Breaking changes require coordinated releases.** If a community interface must change, coordinate with the enterprise repo: update both, tag new versions of both, verify enterprise builds.

5. **Adding new exported types is non-breaking.** New structs, constants, or functions can be added freely.

6. **Removing or renaming exported types is breaking.** Follow the same coordination protocol as interface changes.

---

## Version History

| Version | Change |
|---------|--------|
| v0.1.0 | Initial interface definitions (all interfaces above) |
| v0.1.1 | Moved `internal/audit` and `internal/config` to `pkg/` for enterprise access |
