# AtlasShare Reverse TLS Tunnel Relay - Action Plan

## Overview

A custom reverse TLS tunnel with TCP stream multiplexing, built in Go for commercial use. Designed to bypass CGNAT by having customer nodes dial out to a relay with a public IP.

```
Clients  --->  Relay (public VPS)  <--- outbound TLS tunnel ---  Customer Node
                     |                                                |
              Accepts inbound                                  Dials out
              connections                                      (no inbound needed)
```

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Language | Go | Strong networking stdlib, goroutines, production-proven |
| Authentication | mTLS + session resumption | Zero-trust, cryptographic identity, optimized for scale |
| Multi-tenant | Yes (both models) | Single relay for many nodes, or dedicated relay per customer |
| Target scale | 1000+ concurrent connections | Requires connection pooling, efficient memory use |
| Multiplexing | Custom minimal protocol | Full control, commercial ownership, no dependencies |

---

## Architecture

### Components

```
┌─────────────────────────────────────────────────────────────────────┐
│                           RELAY (VPS)                               │
│                                                                     │
│  ┌─────────────────┐    ┌─────────────────┐    ┌────────────────┐  │
│  │  TLS Listener   │    │  Agent Registry │    │ Client Listener│  │
│  │  (Agent conns)  │───▶│  (node mapping) │◀───│  (TCP ports)   │  │
│  └─────────────────┘    └─────────────────┘    └────────────────┘  │
│           │                      │                      │          │
│           │              ┌───────┴───────┐              │          │
│           └─────────────▶│   Mux Router  │◀─────────────┘          │
│                          └───────────────┘                         │
└─────────────────────────────────────────────────────────────────────┘
                                   │
                          TLS Tunnel (outbound)
                                   │
┌─────────────────────────────────────────────────────────────────────┐
│                        CUSTOMER NODE                                │
│                                                                     │
│  ┌─────────────────┐    ┌─────────────────┐    ┌────────────────┐  │
│  │  Tunnel Agent   │───▶│  Stream Demux   │───▶│ Local Services │  │
│  │  (dials relay)  │    │  (route by ID)  │    │ (Samba, HTTP)  │  │
│  └─────────────────┘    └─────────────────┘    └────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

### Data Flow

1. **Agent connects**: Customer node dials relay over TLS (mTLS auth)
2. **Registration**: Agent registers with node ID, relay stores in registry
3. **Client connects**: External client connects to relay on service port (e.g., 445)
4. **Stream creation**: Relay opens new mux stream to agent
5. **Forwarding**: Agent dials local service, bidirectional copy
6. **Teardown**: Either side closes, stream cleaned up

---

## Custom Wire Protocol

### Design Goals

- Minimal overhead (small header)
- Simple to implement and debug
- Support flow control
- Support keepalive/heartbeat

### Frame Format (12 bytes header)

```
┌────────────┬────────────┬────────────┬────────────┬─────────────────┐
│  Version   │  Command   │  Flags     │  Reserved  │    Stream ID    │
│  (1 byte)  │  (1 byte)  │  (1 byte)  │  (1 byte)  │    (4 bytes)    │
├────────────┴────────────┴────────────┴────────────┴─────────────────┤
│                         Payload Length (4 bytes)                    │
├─────────────────────────────────────────────────────────────────────┤
│                         Payload (variable)                          │
└─────────────────────────────────────────────────────────────────────┘
```

### Fields

| Field | Size | Description |
|-------|------|-------------|
| Version | 1 byte | Protocol version (start at `0x01`) |
| Command | 1 byte | Frame type (see below) |
| Flags | 1 byte | Bitfield for frame flags |
| Reserved | 1 byte | Future use, set to `0x00` |
| Stream ID | 4 bytes | Unique stream identifier (big-endian) |
| Payload Length | 4 bytes | Length of payload (big-endian, max 16MB) |
| Payload | variable | Frame data |

### Commands

| Value | Name | Description |
|-------|------|-------------|
| `0x01` | STREAM_OPEN | Open new stream (payload: target address) |
| `0x02` | STREAM_DATA | Data frame for stream |
| `0x03` | STREAM_CLOSE | Close stream gracefully |
| `0x04` | STREAM_RESET | Abort stream with error |
| `0x05` | PING | Keepalive request |
| `0x06` | PONG | Keepalive response |
| `0x07` | WINDOW_UPDATE | Flow control window increment |
| `0x08` | GOAWAY | Graceful shutdown, no new streams |

### Flags

| Bit | Name | Description |
|-----|------|-------------|
| 0 | FIN | End of stream (no more data) |
| 1 | ACK | Acknowledgment |
| 2-7 | Reserved | Future use |

### Flow Control

- Per-stream receive window (default: 256KB)
- Connection-level window (default: 1MB)
- WINDOW_UPDATE frames increment available window
- Sender blocks when window exhausted

### Stream ID Allocation

- Relay-initiated streams: odd numbers (1, 3, 5, ...)
- Agent-initiated streams: even numbers (2, 4, 6, ...)
- Stream ID 0: reserved for connection-level frames

---

## Authentication: mTLS with Session Resumption

### Certificate Hierarchy

```
AtlasShare Root CA
       │
       ├── Relay Intermediate CA
       │         │
       │         └── relay.atlasshare.io (server cert)
       │
       └── Customer Intermediate CA
                 │
                 └── customer-{id}.atlasshare.io (client cert)
```

### TLS Configuration

```go
tlsConfig := &tls.Config{
    MinVersion:       tls.VersionTLS13,
    ClientAuth:       tls.RequireAndVerifyClientCert,
    ClientCAs:        customerCACertPool,
    Certificates:     []tls.Certificate{relayCert},

    // Session resumption for performance
    SessionTicketsDisabled: false,
    SessionCache:           tls.NewLRUClientSessionCache(10000),
}
```

### Session Resumption Benefits

- Reduces handshake from 2-RTT to 1-RTT (or 0-RTT with TLS 1.3)
- Caches session state, avoids full cert verification on reconnect
- Critical for 1000+ connections at scale

### Certificate Contents

Client cert Subject should include:
- `CN=customer-{uuid}` (unique customer identifier)
- `O=AtlasShare` (organization)
- Custom extension with customer metadata (optional)

---

## Implementation Phases

### Phase 1: Core Protocol (Week 1-2)

- [ ] Define Go structs for frame format
- [ ] Implement frame encoder/decoder
- [ ] Unit tests for framing edge cases
- [ ] Implement stream state machine (OPEN → DATA → CLOSE)
- [ ] Basic flow control

**Deliverable:** Library that can multiplex streams over a single connection

### Phase 2: Agent Implementation (Week 3-4)

- [ ] TLS client with mTLS support
- [ ] Connection establishment and reconnection logic
- [ ] Exponential backoff with jitter
- [ ] Stream demuxer (receive STREAM_OPEN, dial local)
- [ ] Bidirectional copy with proper cleanup
- [ ] Heartbeat (PING/PONG) handling

**Deliverable:** Agent binary that connects to relay and forwards to local services

### Phase 3: Relay Implementation (Week 5-6)

- [ ] TLS server with mTLS verification
- [ ] Agent registry (map customer ID → connection)
- [ ] Client listener (accept TCP on service ports)
- [ ] Stream routing (client → correct agent)
- [ ] Connection pooling for agents
- [ ] Graceful shutdown (GOAWAY)

**Deliverable:** Relay binary that routes client traffic to agents

### Phase 4: Multi-tenancy (Week 7)

- [ ] Customer isolation (streams can't cross customers)
- [ ] Per-customer rate limiting
- [ ] Per-customer connection limits
- [ ] Routing by subdomain or port
- [ ] Metrics per customer

**Deliverable:** Multi-tenant relay with isolation guarantees

### Phase 5: Production Hardening (Week 8-9)

- [ ] Comprehensive logging (structured, leveled)
- [ ] Prometheus metrics (connections, streams, bytes, latency)
- [ ] Health check endpoints
- [ ] Certificate rotation without downtime
- [ ] Graceful restarts
- [ ] Load testing (target: 1000+ concurrent streams)
- [ ] Failure injection testing

**Deliverable:** Production-ready relay and agent

### Phase 6: Operations (Week 10)

- [ ] Systemd service files
- [ ] Docker images
- [ ] Terraform/Ansible for VPS deployment
- [ ] Certificate issuance automation (internal CA)
- [ ] Monitoring dashboards (Grafana)
- [ ] Alerting rules
- [ ] Runbooks for common issues

**Deliverable:** Deployable infrastructure

---

## Project Structure

```
atlasshare-relay/
├── cmd/
│   ├── relay/          # Relay server binary
│   │   └── main.go
│   └── agent/          # Customer agent binary
│       └── main.go
├── pkg/
│   ├── protocol/       # Wire protocol implementation
│   │   ├── frame.go
│   │   ├── frame_test.go
│   │   ├── stream.go
│   │   └── mux.go
│   ├── relay/          # Relay server logic
│   │   ├── server.go
│   │   ├── registry.go
│   │   └── router.go
│   ├── agent/          # Agent logic
│   │   ├── client.go
│   │   ├── tunnel.go
│   │   └── forwarder.go
│   └── auth/           # mTLS and cert handling
│       ├── mtls.go
│       └── certs.go
├── internal/
│   └── config/         # Configuration loading
├── scripts/
│   ├── gen-certs.sh    # Dev certificate generation
│   └── load-test.sh    # Load testing scripts
├── deployments/
│   ├── docker/
│   ├── systemd/
│   └── terraform/
├── docs/
│   ├── protocol.md     # Wire protocol specification
│   └── operations.md   # Operational runbook
├── go.mod
├── go.sum
└── README.md
```

---

## Key Go Packages to Use

| Purpose | Package | Notes |
|---------|---------|-------|
| TLS | `crypto/tls` | Stdlib, full mTLS support |
| TCP | `net` | Stdlib |
| Context | `context` | Cancellation, timeouts |
| Sync | `sync` | Mutexes, WaitGroups |
| Binary encoding | `encoding/binary` | Frame header parsing |
| Logging | `log/slog` | Structured logging (Go 1.21+) |
| Metrics | `github.com/prometheus/client_golang` | Prometheus integration |
| Testing | `testing` + `github.com/stretchr/testify` | Unit tests |

---

## Performance Considerations

### Memory Management

- Reuse byte buffers with `sync.Pool`
- Limit max frame size (16MB default, configurable)
- Per-stream buffer limits with flow control
- Garbage collection tuning (`GOGC`)

### Connection Handling

- One goroutine per stream (read loop)
- Use `io.Copy` or `splice` syscall for zero-copy forwarding
- Connection pooling for agents (multiple conns per customer)
- Idle timeout to clean up stale connections

### Scaling Targets

| Metric | Target |
|--------|--------|
| Concurrent agents | 1,000+ |
| Streams per agent | 100+ |
| Throughput per stream | 100 Mbps |
| Relay memory | < 4GB for 1000 agents |
| Handshake latency | < 50ms (with session resumption) |

---

## Security Considerations

- **No plaintext**: All traffic over TLS 1.3
- **mTLS required**: Both sides authenticate
- **Certificate pinning**: Agents pin relay cert
- **Customer isolation**: Stream IDs scoped to connection
- **Rate limiting**: Per-customer limits on streams/bandwidth
- **Audit logging**: Log all connection/stream events
- **No SMB exposure**: Relay never terminates SMB, just forwards bytes

---

## Testing Strategy

### Unit Tests

- Frame encoding/decoding
- Stream state machine transitions
- Flow control window accounting

### Integration Tests

- Agent ↔ Relay connection establishment
- Stream creation and data forwarding
- Reconnection after disconnect
- Certificate rejection (invalid client)

### Load Tests

- 1000 concurrent agents
- 100 streams per agent
- Sustained throughput for 24 hours
- Chaos testing (random disconnects, latency injection)

### Tools

- `go test` for unit/integration
- `k6` or custom Go load generator
- `toxiproxy` for network fault injection

---

## References

- [HTTP/2 Binary Framing - High Performance Browser Networking](https://hpbn.co/http2/)
- [HashiCorp yamux specification](https://github.com/hashicorp/yamux/blob/master/spec.md)
- [smux protocol (xtaci)](https://github.com/xtaci/smux)
- [Flipt Reverst - Reverse Tunnels over HTTP/3](https://github.com/flipt-io/reverst)
- [Teleport Reverse Tunnel Architecture](https://pkg.go.dev/github.com/gravitational/teleport/lib/reversetunnel)
- [mTLS Authentication Guide - GitGuardian](https://blog.gitguardian.com/mutual-tls-mtls-authentication/)
- [Zero Trust with mTLS - Medium](https://medium.com/beyond-localhost/zero-trust-networking-replacing-api-keys-with-mutual-tls-mtls-b073d79f3b60)
- [TCP Message Framing - CodeProject](https://www.codeproject.com/Articles/37496/TCP-IP-Protocol-Design-Message-Framing)

---

## Resolved Design Questions

### 1. Port Allocation Strategy

**Decision:** Dedicated port per customer

Each customer gets assigned dedicated TCP/UDP ports on the relay. Example:
- Customer A: TCP 10001-10010, UDP 10001-10010
- Customer B: TCP 10011-10020, UDP 10011-10020

**Implementation:**
```go
type CustomerPortAllocation struct {
    CustomerID   string
    TCPPorts     []int  // e.g., [10001, 10002, 10003]
    UDPPorts     []int  // e.g., [10001, 10002, 10003]
    ServiceMap   map[int]string  // port -> service name (e.g., 10001 -> "smb")
}
```

**Considerations:**
- Port range per customer should be configurable
- Relay needs port availability tracking
- Firewall rules generated per customer

---

### 2. UDP Support

**Decision:** Yes, UDP tunneling required

UDP is needed for protocols like WireGuard, DNS, and real-time applications.

**Implementation approach:**

UDP over TCP multiplexing (simpler):
```
UDP packet --> Agent --> Frame as STREAM_DATA --> Relay --> UDP socket --> Client
```

**Wire protocol additions:**

| Value | Name | Description |
|-------|------|-------------|
| `0x09` | UDP_BIND | Request UDP listener on relay |
| `0x0A` | UDP_DATA | UDP datagram (includes source addr in payload) |
| `0x0B` | UDP_UNBIND | Close UDP listener |

**UDP_DATA payload format:**
```
┌────────────────┬────────────────┬─────────────────┐
│  Addr Length   │  Source Addr   │    UDP Data     │
│   (1 byte)     │  (variable)    │   (variable)    │
└────────────────┴────────────────┴─────────────────┘
```

**Note:** UDP-over-TCP adds latency and loses some UDP semantics (out-of-order delivery). For latency-sensitive applications, consider QUIC-based transport in future versions.

---

### 3. Agent Update Strategy

**Decision:** Self-updating agent with rollback support

**Options evaluated:**

| Option | Pros | Cons |
|--------|------|------|
| Manual updates | Simple, full control | Doesn't scale, customer friction |
| Package manager (apt/yum) | Familiar, OS-integrated | Platform-specific, repo maintenance |
| Self-updating binary | Seamless, cross-platform | More complex, security considerations |
| Container-based | Isolation, easy rollback | Requires Docker on customer node |

**Recommended: Self-updating binary** using [minio/selfupdate](https://github.com/minio/selfupdate) patterns (implement in-house for commercial use).

**Update flow:**
```
1. Agent checks relay for version manifest (signed JSON)
2. If newer version available:
   a. Download new binary (HTTPS, checksum verified)
   b. Verify signature (ed25519)
   c. Replace current binary atomically
   d. Restart via systemd or self-exec
3. On failure: rollback to previous binary
```

**Implementation:**
```go
type VersionManifest struct {
    Version     string `json:"version"`
    ReleaseDate string `json:"release_date"`
    Binaries    map[string]BinaryInfo `json:"binaries"` // key: "linux-amd64", "darwin-arm64", etc.
    Signature   string `json:"signature"`
}

type BinaryInfo struct {
    URL       string `json:"url"`
    SHA256    string `json:"sha256"`
    Size      int64  `json:"size"`
}
```

**Security requirements:**
- All updates signed with ed25519 key (private key in secure storage)
- Agent embeds public key at compile time
- HTTPS-only download
- SHA256 verification before apply
- Rollback on crash within 60 seconds of update

**Update check frequency:** Every 6 hours (configurable)

---

### 4. Certificate Lifecycle

**Decision:** 90-day validity with automated rotation

**Industry context:**
- CA/Browser Forum is reducing public TLS certs to 47 days by 2029
- Best practice for internal mTLS: 90-180 days
- Automation is mandatory at scale

**Certificate validity periods:**

| Certificate | Validity | Rotation |
|-------------|----------|----------|
| Root CA | 10 years | Manual, offline ceremony |
| Intermediate CA (Relay) | 3 years | Manual, planned |
| Intermediate CA (Customers) | 3 years | Manual, planned |
| Relay server cert | 90 days | Automated |
| Customer agent cert | 90 days | Automated |

**Automated rotation flow:**
```
1. Agent checks cert expiry on startup and daily
2. When < 30 days remaining:
   a. Generate new CSR (reuse or new key pair)
   b. Submit CSR to AtlasShare control plane API (authenticated)
   c. Control plane signs with Customer Intermediate CA
   d. Agent receives new cert, validates chain
   e. Agent hot-reloads cert (no restart needed)
3. Old cert remains valid until expiry (overlap period)
```

**Implementation:**
```go
type CertRotationConfig struct {
    CheckInterval     time.Duration // e.g., 24 hours
    RenewBeforeExpiry time.Duration // e.g., 30 days
    CSREndpoint       string        // e.g., "https://api.atlasshare.io/v1/certs/renew"
    ReusePrivateKey   bool          // false = generate new key each rotation
}
```

**Control plane requirements:**
- API endpoint for CSR submission
- Automated signing with customer's intermediate CA
- Audit log of all cert issuances
- Revocation support (CRL or OCSP)

**References:**
- [TLS Certificate Lifetimes Reducing to 47 Days - DigiCert](https://www.digicert.com/blog/tls-certificate-lifetimes-will-officially-reduce-to-47-days)
- [mTLS Certificate Rotation - Dapr Docs](https://docs.dapr.io/operations/security/mtls/)

---

### 5. Relay Redundancy

**Decision:** Active-active with shared state

**Options evaluated:**

| Model | Failover Time | Complexity | Cost |
|-------|---------------|------------|------|
| Single relay | N/A (no redundancy) | Low | Low |
| Active-passive | Seconds to minutes | Medium | 2x servers |
| Active-active | Near-instant | High | 2x+ servers |

**Recommended: Active-active** for production, with single relay acceptable for small deployments.

**Architecture:**
```
                    ┌─────────────────┐
                    │  Load Balancer  │
                    │  (TCP/UDP)      │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
        ┌─────▼─────┐  ┌─────▼─────┐  ┌─────▼─────┐
        │  Relay 1  │  │  Relay 2  │  │  Relay N  │
        └─────┬─────┘  └─────┬─────┘  └─────┬─────┘
              │              │              │
              └──────────────┼──────────────┘
                             │
                    ┌────────▼────────┐
                    │  Shared State   │
                    │  (Redis/etcd)   │
                    └─────────────────┘
```

**Shared state requirements:**
- Agent registry: which agent connected to which relay
- Port allocations: prevent conflicts
- Session state: for session resumption across relays

**Agent connection strategy:**
- Agent connects to load balancer VIP
- On disconnect, reconnects (may land on different relay)
- Shared state ensures any relay can route to any agent

**Implementation considerations:**

```go
type AgentRegistry interface {
    Register(customerID string, relayID string, conn *AgentConn) error
    Unregister(customerID string) error
    Lookup(customerID string) (*AgentLocation, error)
    Heartbeat(customerID string) error
}

type AgentLocation struct {
    CustomerID  string
    RelayID     string
    RelayAddr   string    // internal address for relay-to-relay forwarding
    ConnectedAt time.Time
    LastSeen    time.Time
}
```

**Cross-relay forwarding:**
If client connects to Relay 1 but agent is on Relay 2:
```
Client --> Relay 1 --> (internal) --> Relay 2 --> Agent
```

**Heartbeat and failover:**
- Agents send heartbeat every 30 seconds
- Registry entry expires after 90 seconds without heartbeat
- On agent reconnect, registry updated with new relay

**Recommended backing store:** Redis (simple) or etcd (stronger consistency)

**References:**
- [Active-Active vs Active-Passive HA - JSCAPE](https://www.jscape.com/blog/active-active-vs-active-passive-high-availability-cluster)
- [High Availability Architecture - Redis](https://redis.io/blog/high-availability-architecture/)

---

## Updated Implementation Timeline

| Phase | Duration | Deliverable |
|-------|----------|-------------|
| 1. Core Protocol | 2 weeks | Mux library with TCP + UDP framing |
| 2. Agent | 2 weeks | Agent binary with mTLS, reconnection |
| 3. Relay (single) | 2 weeks | Single-node relay with routing |
| 4. Multi-tenancy | 1 week | Customer isolation, port allocation |
| 5. Self-update | 1 week | Agent auto-update system |
| 6. Cert rotation | 1 week | Automated certificate renewal |
| 7. HA (active-active) | 2 weeks | Redis-backed registry, cross-relay routing |
| 8. Production hardening | 2 weeks | Metrics, logging, load testing |

**Total: ~13 weeks**

---

*Document created: 2026-03-03*
*Last updated: 2026-03-03*
