# atlax

[![CI](https://github.com/atlasshare/atlax/actions/workflows/ci.yml/badge.svg)](https://github.com/atlasshare/atlax/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/atlasshare/atlax/branch/main/graph/badge.svg)](https://codecov.io/gh/atlasshare/atlax)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)

A reverse TLS tunnel with TCP stream multiplexing, built in Go. Exposes local services on nodes behind CGNAT through a public relay, using mTLS for authentication and a custom binary wire protocol for performance.

**Production-tested.** Both binaries work end-to-end on AWS with real Samba, web, and API traffic.

## How It Works

```
Client (internet)                     Relay (VPS)                    Customer Node (behind CGNAT)
      |                                  |                                    |
      |--- TCP to relay port 18080 ----->|                                    |
      |                                  |--- mux stream (service: http) ---->|
      |                                  |                  mTLS tunnel        |--- forward to 127.0.0.1:3009
      |                                  |<-- response ----------------------|
      |<-- response ---------------------|                                    |
```

1. The **agent** on the customer node dials out to the relay over mTLS (no inbound ports needed)
2. The **relay** accepts client TCP connections on per-customer dedicated ports
3. Each client connection becomes a multiplexed stream routed to the correct agent
4. The agent forwards the stream to the matching local service
5. Traffic flows bidirectionally until either side closes

## Features

- **Reverse tunnel** -- Agent dials out, bypassing CGNAT and firewalls
- **mTLS authentication** -- Certificate-based identity, TLS 1.3 minimum, zero-trust
- **Stream multiplexing** -- Many client connections over a single tunnel (custom 12-byte wire protocol)
- **Multi-service routing** -- One agent exposes multiple services (Samba, HTTP, API); relay routes by service name in STREAM_OPEN payload
- **Multi-tenant isolation** -- Per-customer port allocation, no cross-tenant routing possible by construction
- **Per-customer limits** -- Configurable stream limits and connection limits per customer
- **Per-IP rate limiting** -- Token bucket rate limiter on client connections
- **Per-port bind address** -- Bind customer ports to 127.0.0.1 for reverse proxy (Caddy/nginx) deployments
- **Graceful shutdown** -- GOAWAY to all agents, stream draining with configurable grace period
- **Structured audit logging** -- Async JSON event emitter for connect/disconnect/auth lifecycle
- **Prometheus metrics** -- Per-customer counters for streams, connections, and rejections

## Quick Start

### Prerequisites

- Go 1.25+
- OpenSSL 3.x

### Build

```bash
git clone https://github.com/atlasshare/atlax.git
cd atlax
make build
make certs-dev   # generate dev mTLS certificates
```

### Run locally

**Terminal 1 -- start an echo server (simulates a local service):**

```bash
socat TCP-LISTEN:9999,reuseaddr,fork EXEC:cat
```

**Terminal 2 -- start the relay:**

```bash
cat > relay.yaml << 'EOF'
server:
  listen_addr: 0.0.0.0:8443
  shutdown_grace_period: 10s
tls:
  cert_file: ./certs/relay.crt
  key_file: ./certs/relay.key
  ca_file: ./certs/root-ca.crt
  client_ca_file: ./certs/customer-ca.crt
customers:
  - id: customer-dev-001
    ports:
      - port: 18080
        service: echo
logging:
  level: debug
  format: text
EOF

./bin/atlax-relay -config relay.yaml
```

**Terminal 3 -- start the agent:**

```bash
cat > agent.yaml << 'EOF'
relay:
  addr: 127.0.0.1:8443
  server_name: relay.atlax.local
  keepalive_interval: 10s
  keepalive_timeout: 5s
tls:
  cert_file: ./certs/agent.crt
  key_file: ./certs/agent.key
  ca_file: ./certs/relay-ca.crt
services:
  - name: echo
    local_addr: 127.0.0.1:9999
    protocol: tcp
logging:
  level: debug
  format: text
EOF

./bin/atlax-agent -config agent.yaml
```

**Terminal 4 -- test:**

```bash
echo "hello atlax" | nc localhost 18080
# Output: hello atlax
```

For cross-machine and AWS deployment, see [Setup and Testing Guide](docs/operations/setup-and-testing.md).

## Architecture

```
                           RELAY (Public VPS)
 +--------------------------------------------------------------------+
 |                                                                    |
 |  TLS Listener (:8443)    Agent Registry    Client Listeners        |
 |  [mTLS agent conns]  --> [customer map] <-- [:18080] [:18070] ...  |
 |          |                     |                    |              |
 |          +--------> Port Router (per-port) <--------+              |
 |                                                                    |
 +--------------------------------------------------------------------+
                    |          mTLS tunnel          |
                    v                               v
              CUSTOMER NODE                  (more nodes...)
 +--------------------------------------------------------------------+
 |                                                                    |
 |  Tunnel Agent --> Stream Demux --> Forwarder --> Local Services     |
 |  [dials relay]    [by service]     [bidi copy]   [Samba, HTTP...]  |
 |                                                                    |
 +--------------------------------------------------------------------+
```

### Wire Protocol

12-byte frame header, binary, big-endian:

| Field | Size | Description |
|-------|------|-------------|
| Version | 1B | Protocol version (0x01) |
| Command | 1B | STREAM_OPEN, STREAM_DATA, STREAM_CLOSE, PING, PONG, WINDOW_UPDATE, GOAWAY, ... |
| Flags | 1B | FIN, ACK |
| Reserved | 1B | 0x00 |
| Stream ID | 4B | Relay-initiated: odd, Agent-initiated: even |
| Payload Length | 4B | Max 16MB per frame |

See [Protocol Documentation](docs/protocol/) for the full specification.

## Multi-Tenant Deployment

Each customer gets dedicated ports on the relay with configurable limits:

```yaml
customers:
  - id: customer-acme
    max_streams: 50
    ports:
      - port: 18080
        service: http
        listen_addr: 127.0.0.1   # only Caddy can reach this
      - port: 18070
        service: api
        listen_addr: 127.0.0.1

  - id: customer-globex
    max_streams: 100
    ports:
      - port: 19080
        service: http
```

For the full isolation model, Caddy reverse proxy pattern, and Prometheus metrics, see [Multi-Tenancy Guide](docs/operations/multi-tenancy.md).

## Community vs Enterprise

| Feature | Community | Enterprise |
|---------|:---------:|:----------:|
| Reverse TLS tunnel | Yes | Yes |
| TCP stream multiplexing | Yes | Yes |
| mTLS authentication (TLS 1.3) | Yes | Yes |
| Multi-service routing | Yes | Yes |
| Multi-tenant isolation | Yes | Yes |
| Per-customer rate limiting | Yes | Yes |
| Structured audit logging | Yes | Yes |
| In-memory agent registry | Yes | Yes |
| Distributed registry (Redis/etcd) | -- | Yes |
| Multi-relay clustering | -- | Yes |
| SIEM audit integration | -- | Yes |
| Web management dashboard | -- | Yes |
| Auto-scaling relay pools | -- | Yes |
| Priority support and SLA | -- | Yes |

## Documentation

- [Setup and Testing](docs/operations/setup-and-testing.md) -- Local, LAN, and AWS deployment guide
- [Multi-Tenancy](docs/operations/multi-tenancy.md) -- Isolation model, limits, Caddy pattern
- [Architecture](docs/architecture/) -- System design and component overview
- [Protocol](docs/protocol/) -- Wire protocol specification
- [Security](docs/security/) -- mTLS, certificate lifecycle, threat model
- [Operations](docs/operations/) -- Deployment, monitoring, troubleshooting
- [Development](docs/development/) -- Phase reports and testing

## Building

```bash
make build       # Build both binaries (bin/atlax-relay, bin/atlax-agent)
make test        # Run all tests with race detector
make lint        # Run golangci-lint
make certs-dev   # Generate dev mTLS certificates
```

Cross-compile for Linux:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/atlax-relay-linux ./cmd/relay/
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/atlax-agent-linux ./cmd/agent/
```

## Project Status

| Phase | Status | Deliverable |
|-------|--------|-------------|
| Phase 0: Scaffold | Complete | Project structure, CI, docs |
| Phase 1: Core Protocol | Complete | Wire protocol, stream multiplexing, flow control |
| Phase 2: Agent | Complete | mTLS auth, tunnel client, service forwarder |
| Phase 3: Relay | Complete | Agent registry, traffic router, relay binary |
| Phase 4: Multi-tenancy | Complete | Per-customer limits, rate limiting, isolation |
| Phase 5: Production Hardening | Planned | Health checks, metrics wiring, load testing |
| Phase 6: Operations | Planned | Terraform, monitoring dashboards, runbooks |

237 tests, 88% coverage, 12 interfaces implemented.

## Contributing

Contributions are welcome. Please read the [development guide](docs/development/) before submitting a pull request.

## Security

For security vulnerabilities, please see [SECURITY.md](SECURITY.md).

## License

Apache 2.0 -- see [LICENSE](LICENSE) for details.
