# Architecture Decision Records

This document captures key architectural decisions for atlax. Each ADR follows a standard format: Context, Decision, Consequences, and Status.

---

## ADR-001: Custom Wire Protocol Over yamux/smux

### Context

atlax needs a multiplexing protocol to carry multiple TCP (and UDP) streams over a single TLS connection between the agent and relay. Existing open-source options include:

- **yamux** (HashiCorp): Mature, production-proven in Consul and Nomad. Implements a subset of HTTP/2 framing. Licensed under MPL-2.0.
- **smux** (xtaci): Lightweight, optimized for low-latency. Licensed under MIT.

Both libraries would accelerate initial development. However, atlax is a commercial product with specific requirements that diverge from general-purpose multiplexers.

### Decision

Implement a custom wire protocol with a 12-byte frame header, 11 commands, per-stream and connection-level flow control, and explicit UDP tunneling support.

### Consequences

**Positive:**
- Full control over the wire format, enabling protocol evolution without upstream dependencies.
- Native UDP tunneling support (UDP_BIND, UDP_DATA, UDP_UNBIND) which neither yamux nor smux provides.
- No license compatibility concerns for commercial distribution.
- Minimal frame overhead (12 bytes vs. yamux's 12 bytes, but with purpose-built semantics).
- Protocol can be optimized specifically for the relay-agent topology (e.g., GOAWAY for graceful relay restarts, stream ID allocation scheme for relay-initiated vs. agent-initiated streams).

**Negative:**
- Higher initial development cost (estimated 2 additional weeks vs. using yamux).
- Must implement and test flow control, keepalive, and error handling from scratch.
- No community of users discovering edge-case bugs; all correctness responsibility is internal.
- Risk of protocol design mistakes that mature libraries have already solved.

**Mitigations:**
- Design inspired by yamux spec and HTTP/2 binary framing (well-understood models).
- Comprehensive test suite including fuzz testing for the frame parser.
- Protocol version field (Version byte) enables backward-compatible evolution.

### Status

Accepted.

---

## ADR-002: mTLS Over API Keys

### Context

Agents connecting to the relay must be authenticated. Two primary approaches were evaluated:

- **API keys:** A shared secret (token) presented in an HTTP header or as part of a handshake payload. Simple to implement, widely understood.
- **mTLS (mutual TLS):** Both sides present X.509 certificates during the TLS handshake. The relay verifies the agent's certificate against a trusted CA, and the agent verifies the relay's certificate.

atlax operates in a zero-trust environment where agents run on customer premises behind CGNAT, connecting to a public relay.

### Decision

Use mutual TLS (mTLS) with TLS 1.3 as the sole authentication mechanism. No API key fallback.

### Consequences

**Positive:**
- Cryptographic identity: each agent is identified by its certificate's `CN=customer-{uuid}`, which cannot be spoofed without the private key.
- Authentication happens at the transport layer before any application data is exchanged, reducing the attack surface.
- No shared secrets to manage, rotate, or risk leaking in logs/headers.
- TLS 1.3 session resumption reduces handshake cost on reconnection (1-RTT or 0-RTT).
- Certificate revocation (CRL/OCSP) provides a standard mechanism to revoke compromised agents.
- Aligns with zero-trust principles: identity is verified on every connection, not just at token issuance.

**Negative:**
- Higher operational complexity: requires a certificate authority, issuance workflow, and rotation automation.
- Customer onboarding requires certificate provisioning (not just sending an API key).
- Certificate expiry (90-day cycle) requires automated renewal to avoid service disruption.
- Debugging TLS handshake failures is harder than debugging "invalid API key" errors.

**Mitigations:**
- Automated certificate renewal via the control plane API (agent renews when < 30 days remain).
- Development CA setup with a single script (`scripts/gen-certs.sh`) for local development.
- Detailed TLS error logging with `log/slog` to aid diagnosis.
- Support for multiple production CA backends (step-ca, Vault, cfssl) documented in [Certificate Operations](../operations/certificate-ops.md).

### Status

Accepted.

---

## ADR-003: Dedicated Ports Per Customer Over SNI Routing

### Context

The relay must route incoming client TCP connections to the correct customer's agent. Two approaches were evaluated:

- **SNI routing:** Clients connect to a single port (e.g., 443). The relay inspects the TLS ClientHello SNI field to determine the target customer. Requires wildcard or per-customer DNS records and TLS certificates.
- **Dedicated ports:** Each customer is assigned a range of TCP ports on the relay (e.g., Customer A gets 10001-10010, Customer B gets 10011-10020). Clients connect to the assigned port.

### Decision

Use dedicated TCP port ranges per customer for client traffic routing.

### Consequences

**Positive:**
- Simple, deterministic routing: port number maps directly to customer and service.
- No dependency on DNS infrastructure for customer resolution.
- Works with any TCP protocol, not just TLS (clients may connect with plaintext protocols like SMB that do not have SNI).
- Clear network isolation: firewall rules can be applied per customer port range.
- No need for per-customer TLS certificates on the client-facing side (the tunnel handles encryption).

**Negative:**
- Port consumption scales linearly with customers and services. A relay with 100 customers at 10 ports each uses 1,000 ports.
- Requires port allocation management and conflict prevention, especially in multi-relay deployments.
- Customers must be told which port to connect to (cannot use a single well-known port).
- Firewall rules grow with customer count.

**Mitigations:**
- Port allocation tracked in the agent registry (prevents conflicts).
- Configurable port range per customer (default: 10 ports, adjustable).
- For deployments where port consumption is a concern, SNI routing can be added as a future option behind a feature flag.
- Port assignments are communicated through the control plane, not manually.

### Status

Accepted.

---

## ADR-004: TLS 1.3 Minimum

### Context

atlax uses TLS for all communication between agents and the relay, and for the control plane API. The minimum TLS version must be chosen to balance security and compatibility.

- **TLS 1.2:** Widely supported, including older clients and operating systems. Allows weaker cipher suites (CBC, SHA-1) unless explicitly restricted.
- **TLS 1.3:** Simplified handshake (1-RTT), mandatory forward secrecy, removed legacy cipher suites (no CBC, RC4, SHA-1), session resumption with 0-RTT option.

Both the relay and agent are controlled software (not arbitrary web browsers), so client compatibility with older TLS versions is not a concern.

### Decision

Require TLS 1.3 as the minimum version for all connections. TLS 1.2 and below are rejected.

```go
tlsConfig := &tls.Config{
    MinVersion: tls.VersionTLS13,
    // ...
}
```

### Consequences

**Positive:**
- All connections use forward secrecy (mandatory in TLS 1.3).
- No negotiation of weak cipher suites; TLS 1.3 cipher suites are all AEAD.
- Simplified handshake (1-RTT, 0-RTT with session resumption) reduces connection latency.
- Reduced attack surface: no CBC padding oracle attacks, no BEAST, no POODLE.
- Smaller configuration surface: no need to curate a cipher suite list.

**Negative:**
- Agents running on very old operating systems (pre-2018 kernels) may not support TLS 1.3. Go's `crypto/tls` has supported TLS 1.3 since Go 1.12 (February 2019), so the Go runtime is not a constraint; the constraint is the OS and any intermediary proxies.
- Some corporate network inspection tools (TLS-intercepting proxies) may not support TLS 1.3 inspection.

**Mitigations:**
- atlax requires Go 1.25+, which fully supports TLS 1.3.
- Agents dial out directly to the relay; corporate proxies typically do not intercept outbound TLS to custom ports.
- Document the TLS 1.3 requirement clearly in deployment prerequisites.

### Status

Accepted.

---

## ADR-005: Community/Enterprise Split Via Interfaces

### Context

atlax is the Community Edition of the AtlasShare relay component, licensed under Apache 2.0. AtlasShare also offers an Enterprise Edition with advanced features (distributed registry, SIEM integration, multi-relay clustering). The two editions must coexist without:

- Polluting the Community Edition with enterprise dependencies (Redis, etcd, Kafka).
- Requiring build tags, conditional compilation, or separate codebases.
- Making it difficult for community contributors to understand or modify the codebase.

### Decision

Define extension points as Go interfaces in the `pkg/` packages. Community Edition provides simple default implementations. Enterprise Edition provides alternative implementations that satisfy the same interfaces, injected at binary initialization time in `cmd/`.

Key interfaces:

```go
// pkg/relay/registry.go
type AgentRegistry interface {
    Register(customerID string, conn *AgentConn) error
    Unregister(customerID string) error
    Lookup(customerID string) (*AgentConn, error)
    Heartbeat(customerID string) error
}

// internal/audit/emitter.go
type Emitter interface {
    Emit(ctx context.Context, event Event) error
}
```

Community implementations:
- `AgentRegistry` -- in-memory `sync.Map` with expiry-based cleanup.
- `Emitter` -- writes structured JSON to `log/slog`.

Enterprise implementations (in a separate private repository):
- `AgentRegistry` -- Redis or etcd backed, with cross-relay lookup.
- `Emitter` -- publishes to Kafka, AWS EventBridge, or SIEM webhook.

### Consequences

**Positive:**
- Clean separation: community contributors never encounter enterprise code.
- No build tags or conditional compilation; the binary is determined entirely by which implementations are wired in `main()`.
- Easy to test: mock any interface for unit testing.
- New extension points can be added by defining a new interface without restructuring the codebase.
- Enterprise code has no license conflict with the Apache 2.0 community code.

**Negative:**
- Interfaces add a layer of indirection. Contributors must understand which implementation is active.
- Interface design must be done carefully up front; changing an interface is a breaking change for enterprise implementations.
- The community in-memory registry is not suitable for production multi-relay deployments (single relay only).

**Mitigations:**
- Document the interface contract thoroughly with godoc comments.
- Keep interfaces small and focused (Interface Segregation Principle).
- Version the interfaces if breaking changes become necessary.
- Document the community/enterprise boundary clearly in [Contributing Guide](contributing.md).

### Status

Accepted.
