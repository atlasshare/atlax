# Multi-Tenancy Guide

## Overview

atlax supports multiple customers on a single relay. Each customer gets dedicated TCP ports, isolated stream routing, and configurable resource limits. This document explains the isolation model, configuration, and deployment patterns.

## Isolation Model

Tenant isolation in atlax is **structural**, not policy-based:

1. **Port-to-customer mapping is static.** Each relay port is assigned to exactly one customer in the config. There is no runtime routing decision -- the port determines the customer.

2. **mTLS identity is cryptographic.** The agent's customer ID is extracted from its certificate CN (`customer-{uuid}`). The relay verifies the cert against the Customer Intermediate CA. Forging an identity requires the CA's private key.

3. **No cross-tenant stream routing.** When a client connects on port 18080, the relay looks up which customer owns that port, then opens a stream on that customer's MuxSession. There is no path for traffic on customer-A's port to reach customer-B's agent.

4. **Agent registry is keyed by customer ID.** Each customer has exactly one active agent connection (or N with `max_connections > 1`). A client connection always routes to the registered agent for that customer.

## Resource Limits

### Per-customer stream limits

```yaml
customers:
  - id: customer-001
    max_streams: 100  # 0 = unlimited
```

When a client connects and the customer's agent already has `max_streams` active streams, the connection is rejected. This prevents a single customer from consuming all relay resources.

### Per-customer connection limits

```yaml
customers:
  - id: customer-001
    max_connections: 1  # default: 1 (replace on reconnect)
```

With `max_connections: 1` (default), a new agent connection replaces the old one with GOAWAY. This is the expected behavior for single-agent deployments.

### Per-source-IP rate limiting

Rate limiting is applied per source IP at the client listener level. All customer ports share a single rate limiter. Configuration is done at the relay startup level (not per-customer in the current version).

When a source IP exceeds the rate limit, connections are rejected immediately (TCP close).

## Per-Port Bind Address

By default, customer ports bind to `0.0.0.0` (all interfaces). For reverse proxy deployments, bind to `127.0.0.1` so only the local proxy can reach the relay port:

```yaml
customers:
  - id: customer-001
    ports:
      - port: 18080
        service: http
        listen_addr: 127.0.0.1  # only Caddy/nginx can reach this
```

### Caddy Reverse Proxy Pattern

The recommended production topology for HTTP services:

```
Internet -> Caddy (port 443, TLS, subdomain routing)
              |
              +-> 127.0.0.1:18080 -> atlax relay -> tunnel -> agent -> web app
              +-> 127.0.0.1:18070 -> atlax relay -> tunnel -> agent -> API
```

Caddy provides:
- TLS termination (HTTPS on the public edge)
- Subdomain routing (`app.example.com` -> port 18080, `api.example.com` -> port 18070)
- HTTP-level rate limiting, caching, and headers
- Automatic certificate management (Let's Encrypt)

The atlax relay provides:
- mTLS tunnel to the agent (encrypted transport)
- Stream multiplexing (many clients over one tunnel)
- Per-customer isolation and resource limits

Example Caddyfile:

```
app.example.com {
    reverse_proxy 127.0.0.1:18080
}

api.example.com {
    reverse_proxy 127.0.0.1:18070
}
```

With `listen_addr: 127.0.0.1` on the relay ports, the customer ports are not accessible from the internet even if the firewall is misconfigured. Defense in depth.

### Non-HTTP Services (SMB, raw TCP)

For non-HTTP protocols, Caddy cannot help (it does not proxy raw TCP by default). These ports must be exposed directly:

```yaml
ports:
  - port: 18445
    service: smb
    # listen_addr defaults to 0.0.0.0 (direct exposure)
```

Use AWS security group rules to restrict source IPs for non-HTTP ports.

## Prometheus Metrics

Per-customer metrics are available when Prometheus is configured:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `atlax_streams_total` | Counter | customer_id | Total streams opened |
| `atlax_streams_active` | Gauge | customer_id | Currently active streams |
| `atlax_connections_total` | Counter | customer_id | Total agent connections |
| `atlax_connections_active` | Gauge | customer_id | Currently connected agents |
| `atlax_clients_rejected_total` | Counter | customer_id, reason | Rejected client connections |

Rejection reasons: `rate_limited`, `stream_limit`, `no_agent`.

## Configuration Example

Complete multi-tenant relay config:

```yaml
server:
  listen_addr: 0.0.0.0:8443
  admin_addr: 127.0.0.1:9090
  max_agents: 100
  idle_timeout: 300s
  shutdown_grace_period: 30s

tls:
  cert_file: /etc/atlax/certs/relay.crt
  key_file: /etc/atlax/certs/relay.key
  ca_file: /etc/atlax/certs/root-ca.crt
  client_ca_file: /etc/atlax/certs/customer-ca.crt

customers:
  - id: customer-acme
    max_connections: 1
    max_streams: 50
    ports:
      - port: 18080
        service: http
        listen_addr: 127.0.0.1
      - port: 18070
        service: api
        listen_addr: 127.0.0.1

  - id: customer-globex
    max_connections: 1
    max_streams: 100
    ports:
      - port: 19080
        service: http
        listen_addr: 127.0.0.1
      - port: 19445
        service: smb
        # Direct exposure for SMB (no reverse proxy)

logging:
  level: info
  format: json
```
