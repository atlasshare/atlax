# atlax

[![CI](https://github.com/atlasshare/atlax/actions/workflows/ci.yml/badge.svg)](https://github.com/atlasshare/atlax/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/atlasshare/atlax/branch/main/graph/badge.svg)](https://codecov.io/gh/atlasshare/atlax)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)

A reverse TLS tunnel with TCP stream multiplexing, built in Go. Exposes local services on nodes behind CGNAT through a public relay, using mTLS for authentication and a custom binary wire protocol for performance.

**Production-tested.** Both binaries work end-to-end on AWS with real Samba, web, and API traffic.

---

## The Problem

You have a server at home or in an office running services (file shares, web apps, APIs). Your ISP uses **CGNAT** (Carrier-Grade NAT), which means your router does not have a public IP address. No one on the internet can reach your services directly -- no port forwarding, no dynamic DNS, nothing works.

Traditional solutions:

| Approach | Limitation |
|----------|-----------|
| Port forwarding | Impossible behind CGNAT -- you don't own the public IP |
| VPN (WireGuard, Tailscale) | Requires VPN client on every device that wants to connect |
| ngrok / Cloudflare Tunnel | Vendor lock-in, HTTP-only (no raw TCP like SMB), usage limits |
| SSH reverse tunnel | Single service per tunnel, no multiplexing, fragile |

**atlax solves this** by running an agent on your server that dials *out* to a relay with a public IP. The relay accepts incoming connections and routes them through the tunnel to your services. No inbound ports needed on your end. Any TCP service works (Samba, HTTP, databases, anything).

## How It Works

```
Client (internet)                     Relay (VPS)                    Your Server (behind CGNAT)
      |                                  |                                    |
      |--- TCP to relay port 18080 ----->|                                    |
      |                                  |--- mux stream (service: http) ---->|
      |                                  |                  mTLS tunnel        |--- forward to 127.0.0.1:3009
      |                                  |<-- response ----------------------|
      |<-- response ---------------------|                                    |
```

1. The **agent** on your server dials out to the relay over mTLS (outbound connection -- works behind any NAT)
2. The **relay** listens on dedicated ports for each customer service
3. When a client connects to a relay port, the relay opens a multiplexed stream inside the existing tunnel
4. The stream carries the service name (e.g., "http") so the agent knows which local service to forward to
5. The agent connects to the local service and copies data bidirectionally
6. The client sees the service as if it were running on the relay's IP

### Key Concepts

**CGNAT (Carrier-Grade NAT):** Your ISP shares one public IP among many customers. You cannot receive inbound connections. atlax bypasses this because the agent initiates the connection outward.

**mTLS (Mutual TLS):** Both the relay and agent present certificates during the TLS handshake. The relay verifies the agent is a legitimate customer. The agent verifies it is talking to the real relay. No passwords, no API keys -- identity is cryptographic.

**Stream Multiplexing:** Instead of one TCP connection per client, atlax sends all client traffic over a single mTLS tunnel by splitting it into numbered streams. Stream 1 might be an SMB session, stream 3 might be an HTTP request -- all sharing one connection. This is efficient and keeps firewall rules simple (one outbound port from the agent).

**Per-Customer Port Isolation:** Each customer gets their own set of relay ports. Traffic on customer A's ports can never reach customer B's agent. This is structural -- the routing code looks up the customer by port, not by any client-provided identifier.

## Features

- **Reverse tunnel** -- Agent dials out, bypassing CGNAT and firewalls. No inbound ports needed on the customer side
- **mTLS authentication** -- Certificate-based identity with TLS 1.3 minimum. Certificate hierarchy: Root CA -> Intermediate CAs -> leaf certs with 90-day validity
- **Stream multiplexing** -- Many client connections over a single tunnel using a custom 12-byte binary wire protocol with flow control
- **Multi-service routing** -- One agent exposes multiple services (Samba, HTTP, API). The relay sends the service name in the STREAM_OPEN frame, and the agent routes to the correct local address
- **Multi-tenant isolation** -- Per-customer port allocation. Cross-tenant routing is impossible by construction (proven by test)
- **Per-customer limits** -- Configurable max streams and max connections per customer
- **Per-IP rate limiting** -- Token bucket rate limiter rejects abusive source IPs immediately
- **Per-port bind address** -- Bind customer ports to `127.0.0.1` so only a reverse proxy (Caddy/nginx) can reach them
- **Graceful shutdown** -- Relay sends GOAWAY to all agents, drains active streams, then closes
- **Structured audit logging** -- Async JSON events for connect, disconnect, auth success/failure
- **Prometheus metrics** -- Per-customer counters for streams, connections, and rejections
- **Heartbeat monitoring** -- Periodic PING/PONG detects dead tunnels
- **Exponential backoff reconnection** -- Agent reconnects with jitter to avoid thundering herd

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

## How To: Deploy on a VPS with a Real Service

This example exposes a web app running on your home server through an AWS EC2 relay.

### 1. Generate certificates with your relay IP

Edit `scripts/gen-certs.sh` and add your VPS IP to the relay cert SAN:

```bash
subjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1,IP:<YOUR_VPS_IP>
```

Then regenerate:

```bash
rm -rf certs/
make certs-dev
```

### 2. Cross-compile and deploy the relay

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/atlax-relay-linux ./cmd/relay/
scp bin/atlax-relay-linux <VPS>:~/atlax/bin/atlax-relay
scp certs/relay.crt certs/relay.key certs/root-ca.crt certs/customer-ca.crt <VPS>:~/atlax/certs/
```

### 3. Create the relay config on the VPS

```yaml
server:
  listen_addr: 0.0.0.0:8443
  shutdown_grace_period: 30s
tls:
  cert_file: ./certs/relay.crt
  key_file: ./certs/relay.key
  ca_file: ./certs/root-ca.crt
  client_ca_file: ./certs/customer-ca.crt
customers:
  - id: customer-dev-001
    max_streams: 100
    ports:
      - port: 18080
        service: http
        listen_addr: 127.0.0.1
logging:
  level: info
  format: json
```

### 4. Deploy the agent to your home server

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/atlax-agent-linux ./cmd/agent/
scp bin/atlax-agent-linux <HOME_SERVER>:~/atlax/bin/atlax-agent
scp certs/agent.crt certs/agent.key certs/relay-ca.crt <HOME_SERVER>:~/atlax/certs/
```

### 5. Create the agent config

```yaml
relay:
  addr: <YOUR_VPS_IP>:8443
  server_name: relay.atlax.local
  keepalive_interval: 30s
  keepalive_timeout: 10s
tls:
  cert_file: ./certs/agent.crt
  key_file: ./certs/agent.key
  ca_file: ./certs/relay-ca.crt
services:
  - name: http
    local_addr: 127.0.0.1:3000
    protocol: tcp
logging:
  level: info
  format: json
```

### 6. Start both and test

```bash
# On VPS:
./bin/atlax-relay -config relay.yaml

# On home server:
./bin/atlax-agent -config agent.yaml

# From anywhere:
curl http://<YOUR_VPS_IP>:18080
```

For systemd units, Caddy reverse proxy setup, and multi-service configuration, see the [Setup and Testing Guide](docs/operations/setup-and-testing.md).

## How To: Expose Multiple Services

One agent can expose multiple local services. Each service gets its own relay port.

**Agent config:**

```yaml
services:
  - name: http
    local_addr: 127.0.0.1:3000
    protocol: tcp
  - name: api
    local_addr: 127.0.0.1:7070
    protocol: tcp
  - name: smb
    local_addr: 127.0.0.1:445
    protocol: tcp
```

**Relay config:**

```yaml
customers:
  - id: customer-dev-001
    ports:
      - port: 18080
        service: http
      - port: 18070
        service: api
      - port: 18445
        service: smb
```

The `service` name in the relay must exactly match the `name` in the agent. The relay sends the service name in the tunnel so the agent knows which local address to forward to. If only one service is configured, the agent routes all traffic to it regardless of the name.

## How To: Put Caddy in Front (HTTPS)

For production HTTP services, put Caddy in front of the relay for TLS termination and subdomain routing. Bind relay ports to `127.0.0.1` so only Caddy can reach them.

**Relay config:**

```yaml
ports:
  - port: 18080
    service: http
    listen_addr: 127.0.0.1   # only Caddy can reach this
```

**Caddyfile:**

```
app.example.com {
    reverse_proxy 127.0.0.1:18080
}

api.example.com {
    reverse_proxy 127.0.0.1:18070
}
```

Now clients access `https://app.example.com` (Caddy handles TLS) and Caddy forwards to the relay on loopback. The tunnel carries the traffic to the agent. The customer ports are not accessible from the internet directly.

See [Multi-Tenancy Guide](docs/operations/multi-tenancy.md) for the full pattern.

## How To: Run Multiple Customers on One Relay

Each customer gets their own ports and their own agent certificate. Traffic is isolated by construction -- port-to-customer mapping is static.

```yaml
customers:
  - id: customer-acme
    max_streams: 50
    ports:
      - port: 18080
        service: http
        listen_addr: 127.0.0.1
      - port: 18070
        service: api
        listen_addr: 127.0.0.1

  - id: customer-globex
    max_streams: 100
    ports:
      - port: 19080
        service: http
      - port: 19445
        service: smb
```

Each customer needs their own agent certificate with `CN=customer-{id}` signed by the Customer Intermediate CA. The relay verifies the certificate and registers the agent under that customer ID.

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

### Certificate Hierarchy

```
AtlasShare Root CA (10-year, offline)
    |
    +--- Relay Intermediate CA (3-year)
    |         |
    |         +--- relay.example.com (90-day server cert)
    |
    +--- Customer Intermediate CA (3-year)
              |
              +--- customer-acme (90-day client cert)
              +--- customer-globex (90-day client cert)
```

The relay trusts the Customer Intermediate CA for agent authentication. The agent trusts the Relay Intermediate CA for server verification. Neither trusts the Root CA directly -- scoped trust prevents cross-domain certificate misuse.

### Wire Protocol

12-byte frame header, binary, big-endian:

| Field | Size | Description |
|-------|------|-------------|
| Version | 1B | Protocol version (0x01) |
| Command | 1B | STREAM_OPEN, STREAM_DATA, STREAM_CLOSE, PING, PONG, WINDOW_UPDATE, GOAWAY |
| Flags | 1B | FIN (half-close), ACK (handshake) |
| Reserved | 1B | 0x00 |
| Stream ID | 4B | Relay-initiated: odd (1,3,5...), Agent-initiated: even (2,4,6...) |
| Payload Length | 4B | Max 16MB per frame |

Each client connection becomes a stream. Streams are multiplexed over the single mTLS tunnel. Flow control windows (per-stream and per-connection) prevent fast senders from overwhelming slow receivers.

See [Protocol Documentation](docs/protocol/) for the full specification.

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

- [Setup and Testing](docs/operations/setup-and-testing.md) -- Local, LAN, and AWS deployment
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
