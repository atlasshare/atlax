# Production Setup Guide

Complete guide for deploying the atlax community edition in production. Covers relay setup on a public VPS, agent setup behind CGNAT, reverse proxy configuration, monitoring, and migration from ad-hoc deployments.

This guide establishes the baseline deployment conventions for all atlax deployments. It is the manual equivalent of `ats setup relay` and `ats setup agent`, which will automate these steps in the future.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Prerequisites](#2-prerequisites)
3. [Certificate Generation](#3-certificate-generation)
4. [Relay Setup](#4-relay-setup)
5. [Agent Setup](#5-agent-setup)
6. [Reverse Proxy (Caddy)](#6-reverse-proxy-caddy)
7. [Monitoring](#7-monitoring)
8. [Migration from Current Setup](#8-migration-from-current-setup)
9. [Security Checklist](#9-security-checklist)
10. [Troubleshooting](#10-troubleshooting)

---

## 1. Overview

atlax is a reverse TLS tunnel with TCP stream multiplexing. Two binaries work together to expose services behind CGNAT through a public relay:

- **`atlax-relay`** -- Runs on a public VPS. Accepts agent mTLS connections on port 8443, accepts client TCP connections on per-customer service ports, and routes traffic through multiplexed streams. Transport and routing only -- never inspects tenant data.
- **`atlax-agent`** -- Runs on customer nodes behind CGNAT. Dials out to the relay over mTLS, receives stream requests, and forwards traffic to local services (Samba, HTTP, APIs). No inbound ports needed.

```
Client (internet) --> Relay (public VPS) <-- mTLS tunnel -- Agent (behind CGNAT) --> Local Services
```

### Conventions

This guide follows the `ats` CLI conventions for all file paths, permissions, and service configuration:

| Convention | Value |
|------------|-------|
| System group | `atlax` (controls file access) |
| Service user | `atlax` (runs daemons) |
| Config directory | `/etc/atlax/` |
| Certificate directory | `/etc/atlax/certs/` |
| Runtime state | `/var/lib/atlax/` |
| Log directory | `/var/log/atlax/` (if not using journald) |
| Binary location | `/usr/local/bin/` |
| Key permissions | `640` (owner + group-readable) |
| Binary names | `atlax-relay`, `atlax-agent` |

---

## 2. Prerequisites

### Hardware Sizing

**Relay (VPS):**

| Agents | Streams | CPU | Memory | File Descriptors |
|--------|---------|-----|--------|------------------|
| 10 | 1,000 | 1 vCPU | 256 MB | 8,192 |
| 100 | 10,000 | 2 vCPU | 1 GB | 32,768 |
| 500 | 50,000 | 4 vCPU | 2 GB | 65,536 |
| 1,000 | 100,000 | 8 vCPU | 4 GB | 131,072 |

For reference, the live deployment runs on an AWS EC2 instance with ~1 GB RAM and 7 GB disk, serving one agent with four services.

**Agent:**

The agent is lightweight. ~30-50 MB memory, negligible CPU when idle. Any machine that can run a Go binary and make outbound TCP connections works.

### Software

| Requirement | Purpose |
|-------------|---------|
| Linux with systemd | Service management (Ubuntu 22.04+, Debian 12+, Arch, RHEL 9+) |
| Go 1.25+ | Building from source (or use prebuilt binaries) |
| OpenSSL 3.x or step-ca | Certificate generation |
| Caddy 2.x (optional) | HTTPS reverse proxy for customer ports |
| make | Building binaries |

### Network

**Relay:**

| Direction | Port | Protocol | Purpose |
|-----------|------|----------|---------|
| Inbound | 8443 | TCP | Agent mTLS connections |
| Inbound | 80 | TCP | ACME HTTP-01 challenge (Caddy) |
| Inbound | 443 | TCP | HTTPS (Caddy reverse proxy) |
| Loopback | 9090 | TCP | Admin API / Prometheus metrics |
| Loopback | customer ports | TCP | Customer service ports (behind Caddy) |

**Agent:**

| Direction | Port | Protocol | Purpose |
|-----------|------|----------|---------|
| Outbound | 8443 | TCP | mTLS tunnel to relay |
| Loopback | varies | TCP | Local service forwarding |

The agent makes only outbound connections. No inbound ports are required -- this is why atlax works behind CGNAT.

---

## 3. Certificate Generation

atlax uses mTLS with a two-tier CA hierarchy:

```
Root CA (10yr, offline)
  |
  +-- Relay Intermediate CA (3yr)
  |     +-- relay cert (90-day)
  |
  +-- Customer Intermediate CA (3yr)
        +-- customer-{id} cert (90-day)
```

### Using the gen-certs Script

The atlax repo includes a script for generating development certificates. For production, use a proper CA (step-ca, Vault PKI, cfssl), but the script structure is the same.

```bash
cd /path/to/atlax
CERT_OUTPUT_DIR=./certs bash scripts/gen-certs.sh
```

This generates the following files:

| File | Purpose |
|------|---------|
| `root-ca.crt` / `root-ca.key` | Root CA (keep key offline in production) |
| `relay-ca.crt` / `relay-ca.key` | Relay Intermediate CA |
| `customer-ca.crt` / `customer-ca.key` | Customer Intermediate CA |
| `relay.crt` / `relay.key` | Relay server certificate (CN=relay.atlax.local) |
| `agent.crt` / `agent.key` | Agent client certificate (CN=customer-dev-001) |
| `relay-chain.crt` | Relay cert + Relay CA cert (for TLS serving) |
| `agent-chain.crt` | Agent cert + Customer CA cert (for mTLS client auth) |

### Why Chain Certificates Are Required

TLS certificate verification walks the chain from the leaf certificate up to a trusted root. With intermediate CAs, the verifying party needs the full chain: leaf cert + intermediate CA cert.

- The **agent** connects to the relay and verifies the relay's certificate. It needs the relay's chain: `relay.crt` + `relay-ca.crt`. If the relay presents only `relay.crt` (a bare cert), the agent cannot build the path from `relay.crt` through `relay-ca.crt` to `root-ca.crt`, and the TLS handshake fails with a certificate verification error.
- The **relay** verifies the agent's client certificate. It needs the agent's chain: `agent.crt` + `customer-ca.crt`. If the agent presents only `agent.crt`, the relay cannot verify it was signed by `customer-ca.crt`.

**Using bare certificates instead of chain certificates is the most common cause of TLS handshake failures.**

### Creating Chain Certificates Manually

If you generated certificates individually (not with gen-certs.sh), create chains by concatenation. Order matters -- leaf first, then intermediate:

```bash
# Relay chain: relay cert + relay intermediate CA
cat relay.crt relay-ca.crt > relay-chain.crt

# Agent chain: agent cert + customer intermediate CA
cat agent.crt customer-ca.crt > agent-chain.crt
```

Verify the chain:

```bash
# Verify relay chain against root CA
openssl verify -CAfile root-ca.crt -untrusted relay-ca.crt relay.crt

# Verify agent chain against root CA
openssl verify -CAfile root-ca.crt -untrusted customer-ca.crt agent.crt
```

Both commands should output the certificate path ending with `: OK`.

---

## 4. Relay Setup

This section uses the live relay (AWS EC2, relay.example.com, Ubuntu 24.04) as the running example.

### 4.1 System Preparation

Create the system group and service user:

```bash
sudo groupadd --system atlax
sudo useradd --system --no-create-home --shell /usr/sbin/nologin -g atlax atlax
```

Create the directory structure:

```bash
sudo mkdir -p /etc/atlax/certs
sudo mkdir -p /var/lib/atlax
sudo mkdir -p /var/log/atlax
```

Set ownership and permissions:

```bash
sudo chown -R root:atlax /etc/atlax
sudo chown -R atlax:atlax /var/lib/atlax
sudo chown -R atlax:atlax /var/log/atlax
sudo chmod -R g+r /etc/atlax
sudo chmod 750 /etc/atlax/certs
```

### 4.2 Binary Installation

Build from source:

```bash
git clone https://github.com/atlasshare/atlax.git /tmp/atlax-build
cd /tmp/atlax-build
make build
sudo install -m 755 bin/atlax-relay /usr/local/bin/atlax-relay
```

Verify:

```bash
atlax-relay --version
```

### 4.3 Certificate Installation

Copy the certificates to the relay. The relay needs:

| File | Source | Purpose |
|------|--------|---------|
| `relay-chain.crt` | `relay.crt` + `relay-ca.crt` | TLS certificate chain presented to agents |
| `relay.key` | Private key | TLS private key |
| `root-ca.crt` | Root CA | Verifying full certificate chains |
| `customer-ca.crt` | Customer Intermediate CA | Verifying agent client certificates |

```bash
sudo cp relay-chain.crt relay.key root-ca.crt customer-ca.crt /etc/atlax/certs/
sudo chown root:atlax /etc/atlax/certs/*
sudo chmod 644 /etc/atlax/certs/relay-chain.crt /etc/atlax/certs/root-ca.crt /etc/atlax/certs/customer-ca.crt
sudo chmod 640 /etc/atlax/certs/relay.key
```

**Use `relay-chain.crt`, not `relay.crt`.** Bare certificates cause TLS verification failures. See [Section 3](#3-certificate-generation) for details.

### 4.4 Configuration

Create `/etc/atlax/relay.yaml`. This example mirrors the live deployment (customer-dev-001 with four services behind Caddy):

```yaml
server:
  listen_addr: "0.0.0.0:8443"
  admin_addr: "127.0.0.1:9090"
  max_agents: 100
  max_streams_per_agent: 100
  idle_timeout: 300s
  shutdown_grace_period: 30s

tls:
  cert_file: /etc/atlax/certs/relay-chain.crt
  key_file: /etc/atlax/certs/relay.key
  ca_file: /etc/atlax/certs/root-ca.crt
  client_ca_file: /etc/atlax/certs/customer-ca.crt
  min_version: "1.3"

customers:
  - id: "customer-dev-001"
    max_connections: 1
    max_streams: 100
    ports:
      - port: 18445
        service: "smb"
        listen_addr: "0.0.0.0"
        description: "SMB file sharing"

      - port: 18080
        service: "http"
        listen_addr: "127.0.0.1"
        description: "Dashboard web UI"

      - port: 18070
        service: "api"
        listen_addr: "127.0.0.1"
        description: "Dashboard API"

      - port: 18090
        service: "portfolio"
        listen_addr: "127.0.0.1"
        description: "Portfolio site"

logging:
  level: info
  format: json

metrics:
  enabled: true
  path: /metrics
  prefix: atlax
```

Key points:

- **`cert_file` uses `relay-chain.crt`** (chain cert, not bare cert).
- **`ca_file` is `root-ca.crt`** for full chain validation from agent certs up to the root.
- **`listen_addr: "127.0.0.1"`** on ports 18080, 18070, 18090 because Caddy reverse-proxies those. Only Caddy (running on the same host) can reach them. Port 18445 (SMB) uses `0.0.0.0` because SMB clients connect directly (no HTTP proxy).
- **`customers[].id`** must exactly match the CN in the agent's certificate.
- **`ports[].service`** must exactly match the agent's `services[].name`. This is the most common source of errors.

Set permissions on the config file:

```bash
sudo chown root:atlax /etc/atlax/relay.yaml
sudo chmod 640 /etc/atlax/relay.yaml
```

### 4.5 Firewall

**UFW (Ubuntu):**

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp comment 'SSH'
sudo ufw allow 80/tcp comment 'HTTP (ACME challenge)'
sudo ufw allow 443/tcp comment 'HTTPS (Caddy)'
sudo ufw allow 8443/tcp comment 'Atlax agent mTLS'
sudo ufw allow 18445/tcp comment 'Atlax SMB (direct access)'
sudo ufw --force enable
```

Customer ports bound to `127.0.0.1` (18080, 18070, 18090) do not need firewall rules -- they are only reachable from localhost. Only expose ports that clients connect to directly (like SMB on 18445).

**AWS Security Groups:**

```bash
aws ec2 authorize-security-group-ingress \
  --group-id sg-XXXXXXXX \
  --protocol tcp --port 8443 --cidr 0.0.0.0/0 \
  --tag-specifications 'ResourceType=security-group-rule,Tags=[{Key=Name,Value=Atlax Agent mTLS}]'

aws ec2 authorize-security-group-ingress \
  --group-id sg-XXXXXXXX \
  --protocol tcp --port 443 --cidr 0.0.0.0/0 \
  --tag-specifications 'ResourceType=security-group-rule,Tags=[{Key=Name,Value=HTTPS}]'

aws ec2 authorize-security-group-ingress \
  --group-id sg-XXXXXXXX \
  --protocol tcp --port 80 --cidr 0.0.0.0/0 \
  --tag-specifications 'ResourceType=security-group-rule,Tags=[{Key=Name,Value=HTTP ACME}]'

aws ec2 authorize-security-group-ingress \
  --group-id sg-XXXXXXXX \
  --protocol tcp --port 18445 --cidr 0.0.0.0/0 \
  --tag-specifications 'ResourceType=security-group-rule,Tags=[{Key=Name,Value=Atlax SMB}]'
```

### 4.6 Systemd Service

Copy the hardened unit file from the repo:

```bash
sudo cp deployments/systemd/atlax-relay.service /etc/systemd/system/atlax-relay.service
```

Or if deploying from a built binary without the repo, create `/etc/systemd/system/atlax-relay.service`:

```ini
[Unit]
Description=atlax relay server - reverse TLS tunnel with TCP stream multiplexing
Documentation=https://github.com/atlasshare/atlax
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=atlax
Group=atlax

ExecStart=/usr/local/bin/atlax-relay -config /etc/atlax/relay.yaml
Restart=always
RestartSec=5s

EnvironmentFile=-/etc/atlax/relay.env

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
RestrictRealtime=yes
MemoryDenyWriteExecute=yes
LockPersonality=yes

# Creates /run/atlax/ owned by service user
RuntimeDirectory=atlax
ReadWritePaths=/var/lib/atlax /var/log/atlax
ReadOnlyPaths=/etc/atlax

# Allow binding to privileged ports (443, etc.)
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# Resource limits
LimitNOFILE=65536
LimitNPROC=4096

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=atlax-relay

[Install]
WantedBy=multi-user.target
```

Hardening notes:

- `ProtectSystem=strict` -- Mounts the filesystem read-only except for explicit `ReadWritePaths`.
- `ProtectHome=yes` -- Makes `/home`, `/root`, `/run/user` inaccessible.
- `NoNewPrivileges=yes` -- Prevents privilege escalation via setuid binaries.
- `PrivateDevices=yes` -- Removes access to physical devices.
- `MemoryDenyWriteExecute=yes` -- Prevents JIT-style exploits.
- `CAP_NET_BIND_SERVICE` -- Allows binding to ports below 1024 if needed. Can be removed if all ports are above 1024.
- `LimitNOFILE=65536` -- Each client connection uses a file descriptor. Scale this with expected connections.

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable atlax-relay
sudo systemctl start atlax-relay
```

### 4.7 Verification

```bash
# Service status
sudo systemctl status atlax-relay

# Logs (last 50 lines)
journalctl -u atlax-relay -n 50 --no-pager

# Health check
curl -s http://localhost:9090/healthz
# Expected: 200 OK

# Readiness check
curl -s http://localhost:9090/readyz
# Expected: 200 OK

# Prometheus metrics
curl -s http://localhost:9090/metrics | head -20

# List connected agents (should be empty before agent setup)
curl -s http://localhost:9090/agents

# List port-to-customer mappings
curl -s http://localhost:9090/ports
```

---

## 5. Agent Setup

This section uses the live agent (Arch Linux, agent.local, behind CGNAT) as the running example.

### 5.1 System Preparation

Create the system group and service user:

```bash
sudo groupadd --system atlax
sudo useradd --system --no-create-home --shell /usr/sbin/nologin -g atlax atlax
```

Create the directory structure:

```bash
sudo mkdir -p /etc/atlax/certs
sudo mkdir -p /var/lib/atlax
sudo mkdir -p /var/log/atlax
```

Set ownership and permissions:

```bash
sudo chown -R root:atlax /etc/atlax
sudo chown -R atlax:atlax /var/lib/atlax
sudo chown -R atlax:atlax /var/log/atlax
sudo chmod -R g+r /etc/atlax
sudo chmod 750 /etc/atlax/certs
```

### 5.2 Binary Installation

Build from source:

```bash
git clone https://github.com/atlasshare/atlax.git /tmp/atlax-build
cd /tmp/atlax-build
make build
sudo install -m 755 bin/atlax-agent /usr/local/bin/atlax-agent
```

Verify:

```bash
atlax-agent --version
```

### 5.3 Certificate Installation

The agent needs:

| File | Source | Purpose |
|------|--------|---------|
| `agent-chain.crt` | `agent.crt` + `customer-ca.crt` | mTLS client certificate chain |
| `agent.key` | Private key | mTLS client private key |
| `root-ca.crt` | Root CA | Verifying relay server certificate chain |

```bash
sudo cp agent-chain.crt agent.key root-ca.crt /etc/atlax/certs/
sudo chown root:atlax /etc/atlax/certs/*
sudo chmod 644 /etc/atlax/certs/agent-chain.crt /etc/atlax/certs/root-ca.crt
sudo chmod 640 /etc/atlax/certs/agent.key
```

**Use `agent-chain.crt`, not `agent.crt`.** The relay verifies the agent's full certificate chain. A bare cert causes mTLS handshake failures.

**Use `root-ca.crt` as `ca_file`, not `relay-ca.crt`.** The agent verifies the relay's certificate chain from the leaf through the relay intermediate CA up to the root. Using `relay-ca.crt` as the CA file works only by accident (it trusts the intermediate as a root) and breaks if the relay cert is reissued under a different intermediate.

### 5.4 Configuration

Create `/etc/atlax/agent.yaml`. This example mirrors the live deployment:

```yaml
relay:
  addr: "relay.example.com:8443"
  server_name: "relay.atlax.local"
  reconnect_interval: 5s
  reconnect_max_backoff: 300s
  reconnect_jitter: true
  keepalive_interval: 30s
  keepalive_timeout: 10s

tls:
  cert_file: /etc/atlax/certs/agent-chain.crt
  key_file: /etc/atlax/certs/agent.key
  ca_file: /etc/atlax/certs/root-ca.crt
  min_version: "1.3"

services:
  - name: "smb"
    local_addr: "127.0.0.1:445"
    protocol: "tcp"
    description: "Samba file sharing"

  - name: "http"
    local_addr: "127.0.0.1:3009"
    protocol: "tcp"
    description: "Dashboard web UI"

  - name: "api"
    local_addr: "127.0.0.1:7070"
    protocol: "tcp"
    description: "Dashboard API"

  - name: "portfolio"
    local_addr: "127.0.0.1:3000"
    protocol: "tcp"
    description: "Portfolio site"

logging:
  level: info
  format: json
```

Key points:

- **`cert_file` uses `agent-chain.crt`** (chain cert, not bare cert).
- **`ca_file` is `root-ca.crt`** (root CA, not `relay-ca.crt`).
- **`server_name`** must match the CN or SAN in the relay certificate. The gen-certs script sets SAN to `relay.atlax.local`. If your relay cert has a different CN/SAN, update this field.
- **`relay.addr`** uses the relay's public IP or DNS name. Use a DNS name in production for easier relay migration.

Set permissions on the config file:

```bash
sudo chown root:atlax /etc/atlax/agent.yaml
sudo chmod 640 /etc/atlax/agent.yaml
```

### 5.5 Service Name Matching

**This is the most common source of errors.** The `services[].name` on the agent must exactly match `customers[].ports[].service` on the relay for the same customer.

```
Relay config (relay.yaml)                Agent config (agent.yaml)
-------------------------------          ----------------------------
customers:                               services:
  - id: customer-dev-001                   - name: smb           <-- MUST MATCH
    ports:                                   local_addr: 127.0.0.1:445
      - port: 18445
        service: smb          <-- MUST MATCH
                                           - name: http          <-- MUST MATCH
      - port: 18080                          local_addr: 127.0.0.1:3009
        service: http         <-- MUST MATCH

      - port: 18070                        - name: api           <-- MUST MATCH
        service: api          <-- MUST MATCH   local_addr: 127.0.0.1:7070

      - port: 18090                        - name: portfolio     <-- MUST MATCH
        service: portfolio    <-- MUST MATCH   local_addr: 127.0.0.1:3000
```

When a client connects to relay port 18080:

1. Relay looks up port 18080 and finds customer `customer-dev-001`, service `http`.
2. Relay sends a `STREAM_OPEN` frame with payload `http` to the agent.
3. Agent receives `http`, looks it up in `services[]`, finds `127.0.0.1:3009`.
4. Agent opens a TCP connection to `127.0.0.1:3009` and bridges the stream.

If the names do not match, the relay log shows `"unknown service"` and the stream is reset.

### 5.6 Systemd Service

Copy the hardened unit file from the repo:

```bash
sudo cp deployments/systemd/atlax-agent.service /etc/systemd/system/atlax-agent.service
```

Or create `/etc/systemd/system/atlax-agent.service`:

```ini
[Unit]
Description=atlax tunnel agent - reverse TLS tunnel client
Documentation=https://github.com/atlasshare/atlax
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=atlax
Group=atlax

ExecStart=/usr/local/bin/atlax-agent -config /etc/atlax/agent.yaml
Restart=always
RestartSec=5s

EnvironmentFile=-/etc/atlax/agent.env

WatchdogSec=30s

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
RestrictRealtime=yes
MemoryDenyWriteExecute=yes
LockPersonality=yes

ReadWritePaths=/var/lib/atlax /var/log/atlax
ReadOnlyPaths=/etc/atlax

# No privileged ports needed (outbound connections only)
CapabilityBoundingSet=

# Resource limits
LimitNOFILE=65536
LimitNPROC=4096

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=atlax-agent

[Install]
WantedBy=multi-user.target
```

Key differences from the relay unit:

- **`WatchdogSec=30s`** -- The agent sends systemd watchdog notifications. If the agent stops responding for 30 seconds, systemd restarts it.
- **No `CAP_NET_BIND_SERVICE`** -- The agent only makes outbound connections; it never binds privileged ports.
- **No `RuntimeDirectory`** -- The agent does not use a Unix socket.

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable atlax-agent
sudo systemctl start atlax-agent
```

### 5.7 Verification

```bash
# Service status
sudo systemctl status atlax-agent

# Logs (look for "connected to relay")
journalctl -u atlax-agent -n 50 --no-pager

# Verify from the relay side -- agent should appear
curl -s http://localhost:9090/agents    # Run on the relay host
# Expected: customer-dev-001 listed as connected

# Check metrics for connected agents
curl -s http://localhost:9090/metrics | grep atlax_relay_agents
# Expected: atlax_relay_agents_connected 1
```

---

## 6. Reverse Proxy (Caddy)

Caddy provides automatic HTTPS with Let's Encrypt for customer service ports. This is the recommended setup when exposing HTTP services through the tunnel.

### 6.1 Install Caddy

**Ubuntu/Debian:**

```bash
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install caddy
```

**Arch Linux:**

```bash
sudo pacman -S caddy
```

### 6.2 Caddyfile

Create `/etc/caddy/Caddyfile`. This example mirrors the live deployment:

```
app.example.com {
    reverse_proxy localhost:18090
    encode gzip zstd

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "SAMEORIGIN"
        Referrer-Policy "strict-origin-when-cross-origin"
        -Server
    }
}

tower.app.example.com {
    @api path /api/*
    reverse_proxy @api localhost:18070
    reverse_proxy localhost:18080

    encode gzip zstd

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "SAMEORIGIN"
        Referrer-Policy "strict-origin-when-cross-origin"
        -Server
    }
}
```

### 6.3 Why listen_addr Must Be 127.0.0.1

When using Caddy (or any reverse proxy), set `listen_addr: "127.0.0.1"` on the customer ports in `relay.yaml`:

```yaml
ports:
  - port: 18080
    service: "http"
    listen_addr: "127.0.0.1"    # Only Caddy can reach this
```

This ensures:

- Customer service ports are not directly reachable from the internet.
- All HTTP traffic goes through Caddy, which handles TLS termination, security headers, and access logging.
- The only public-facing ports are 443 (Caddy HTTPS), 80 (ACME challenges), and 8443 (agent mTLS).

Ports that clients connect to directly (like SMB on 18445) should use `listen_addr: "0.0.0.0"` since SMB clients do not go through an HTTP proxy.

### 6.4 Start Caddy

```bash
sudo systemctl enable caddy
sudo systemctl start caddy

# Verify
curl -I https://app.example.com
curl -I https://tower.app.example.com
```

Caddy automatically obtains and renews Let's Encrypt certificates. Ensure ports 80 and 443 are open in the firewall and security group.

---

## 7. Monitoring

### 7.1 Admin API Endpoints

The relay exposes an admin API on the address configured in `server.admin_addr` (default `127.0.0.1:9090`):

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/healthz` | GET | Liveness probe -- returns 200 if the process is running |
| `/readyz` | GET | Readiness probe -- returns 200 if the relay is accepting connections |
| `/metrics` | GET | Prometheus metrics exposition |
| `/agents` | GET | List connected agents with customer ID and connection time |
| `/agents/{customerID}` | DELETE | Disconnect an agent (sends GOAWAY frame) |
| `/ports` | GET | List port-to-customer-to-service mappings |
| `/ports` | POST | Add a port mapping at runtime |
| `/ports/{port}` | DELETE | Remove a port mapping |
| `/stats` | GET | Relay uptime, total streams opened/closed |

### 7.2 Prometheus Scrape Config

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: atlax-relay
    static_configs:
      - targets: ['localhost:9090']
    scrape_interval: 15s
    metrics_path: /metrics
```

If Prometheus runs on a different host, either:
- SSH tunnel: `ssh -L 9090:localhost:9090 relay-host`
- Change `admin_addr` to `0.0.0.0:9090` and restrict with firewall rules (less preferred)

### 7.3 Key Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `atlax_relay_agents_connected` | Gauge | Number of currently connected agents |
| `atlax_relay_streams_active` | Gauge | Number of active streams across all agents |
| `atlax_relay_streams_total` | Counter | Total streams opened since startup |
| `atlax_relay_bytes_relayed_total` | Counter | Total bytes relayed (labeled by direction) |
| `atlax_relay_connections_total` | Counter | Total client connections by customer |
| `atlax_relay_handshake_errors_total` | Counter | TLS handshake failures |
| `atlax_relay_stream_errors_total` | Counter | Stream-level errors |

### 7.4 Grafana Dashboard

A pre-built Grafana dashboard is available at `deployments/grafana/atlax-relay-dashboard.json`. Import it into Grafana and point it at your Prometheus data source.

---

## 8. Migration from Current Setup

This section provides step-by-step instructions for migrating the live relay and agent from their current ad-hoc home-directory deployments to the standard `/etc/atlax/` layout with hardened systemd units.

### 8.1 Migrate the Relay (AWS EC2, Ubuntu 24.04)

The current relay runs as user `relay-user` with files in `~/atlax/`. It uses bare `relay.crt` instead of `relay-chain.crt` and has a minimal systemd unit without hardening.

**Step 1: Create group, user, and directories.**

```bash
sudo groupadd --system atlax
sudo useradd --system --no-create-home --shell /usr/sbin/nologin -g atlax atlax
sudo mkdir -p /etc/atlax/certs
sudo mkdir -p /var/lib/atlax
sudo mkdir -p /var/log/atlax
sudo chown -R root:atlax /etc/atlax
sudo chown -R atlax:atlax /var/lib/atlax /var/log/atlax
sudo chmod -R g+r /etc/atlax
sudo chmod 750 /etc/atlax/certs
```

**Step 2: Create chain certificates.**

The current setup uses bare `relay.crt`. Create the chain cert from the existing files:

```bash
cd /home/relay-user/atlax/certs
cat relay.crt relay-ca.crt > relay-chain.crt
```

**Step 3: Copy certificates with correct permissions.**

```bash
sudo cp /home/relay-user/atlax/certs/relay-chain.crt /etc/atlax/certs/
sudo cp /home/relay-user/atlax/certs/relay.key /etc/atlax/certs/
sudo cp /home/relay-user/atlax/certs/root-ca.crt /etc/atlax/certs/
sudo cp /home/relay-user/atlax/certs/customer-ca.crt /etc/atlax/certs/
sudo chown root:atlax /etc/atlax/certs/*
sudo chmod 644 /etc/atlax/certs/relay-chain.crt /etc/atlax/certs/root-ca.crt /etc/atlax/certs/customer-ca.crt
sudo chmod 640 /etc/atlax/certs/relay.key
```

**Step 4: Create the new config file.**

The key changes from the old config:
- All paths are absolute (`/etc/atlax/certs/...` instead of relative paths)
- `cert_file` uses `relay-chain.crt` instead of bare `relay.crt`
- `ca_file` uses `root-ca.crt` for proper chain validation

Create `/etc/atlax/relay.yaml` with the contents from [Section 4.4](#44-configuration).

```bash
sudo chown root:atlax /etc/atlax/relay.yaml
sudo chmod 640 /etc/atlax/relay.yaml
```

**Step 5: Install the binary.**

```bash
sudo cp /home/relay-user/atlax/bin/atlax-relay /usr/local/bin/atlax-relay
sudo chmod 755 /usr/local/bin/atlax-relay
```

Or build a fresh binary (see [Section 4.2](#42-binary-installation)).

**Step 6: Install the hardened systemd unit.**

```bash
# Back up the old unit
sudo cp /etc/systemd/system/atlax-relay.service /etc/systemd/system/atlax-relay.service.bak

# Install the new hardened unit
sudo cp deployments/systemd/atlax-relay.service /etc/systemd/system/atlax-relay.service
sudo systemctl daemon-reload
```

**Step 7: Switch services.**

```bash
# Stop the old service
sudo systemctl stop atlax-relay

# Start the new service
sudo systemctl start atlax-relay

# Verify
sudo systemctl status atlax-relay
curl -s http://localhost:9090/healthz
curl -s http://localhost:9090/readyz
```

**Step 8: Verify the agent reconnects.**

After the relay restarts, the agent will reconnect automatically (exponential backoff starting at 5 seconds). Check:

```bash
# On the relay
curl -s http://localhost:9090/agents
# Expected: customer-dev-001 connected

# Test a service through Caddy
curl -I https://app.example.com
```

**Step 9: Clean up the old home-directory files.**

After confirming everything works:

```bash
# Remove the old setup (optional, do this after a soak period)
sudo rm -rf /home/relay-user/atlax/
sudo userdel relay-user
```

### 8.2 Migrate the Agent (Arch Linux)

The current agent runs as user `agent-user` with files in `~/atlax/`. It uses bare `agent.crt` instead of `agent-chain.crt` and uses `relay-ca.crt` as the CA file instead of `root-ca.crt`.

**Step 1: Create group, user, and directories.**

```bash
sudo groupadd --system atlax
sudo useradd --system --no-create-home --shell /usr/sbin/nologin -g atlax atlax
sudo mkdir -p /etc/atlax/certs
sudo mkdir -p /var/lib/atlax
sudo mkdir -p /var/log/atlax
sudo chown -R root:atlax /etc/atlax
sudo chown -R atlax:atlax /var/lib/atlax /var/log/atlax
sudo chmod -R g+r /etc/atlax
sudo chmod 750 /etc/atlax/certs
```

**Step 2: Create chain certificates.**

```bash
cd /home/agent-user/atlax/certs
cat agent.crt customer-ca.crt > agent-chain.crt
```

**Step 3: Copy certificates with correct permissions.**

```bash
sudo cp /home/agent-user/atlax/certs/agent-chain.crt /etc/atlax/certs/
sudo cp /home/agent-user/atlax/certs/agent.key /etc/atlax/certs/
sudo cp /home/agent-user/atlax/certs/root-ca.crt /etc/atlax/certs/
sudo chown root:atlax /etc/atlax/certs/*
sudo chmod 644 /etc/atlax/certs/agent-chain.crt /etc/atlax/certs/root-ca.crt
sudo chmod 640 /etc/atlax/certs/agent.key
```

**Step 4: Create the new config file.**

The key changes from the old config:
- All paths are absolute (`/etc/atlax/certs/...`)
- `cert_file` uses `agent-chain.crt` instead of bare `agent.crt`
- `ca_file` uses `root-ca.crt` instead of `relay-ca.crt`

Create `/etc/atlax/agent.yaml` with the contents from [Section 5.4](#54-configuration).

```bash
sudo chown root:atlax /etc/atlax/agent.yaml
sudo chmod 640 /etc/atlax/agent.yaml
```

**Step 5: Install the binary.**

```bash
sudo cp /home/agent-user/atlax/bin/atlax-agent /usr/local/bin/atlax-agent
sudo chmod 755 /usr/local/bin/atlax-agent
```

**Step 6: Install the hardened systemd unit.**

```bash
sudo cp /etc/systemd/system/atlax-agent.service /etc/systemd/system/atlax-agent.service.bak
sudo cp deployments/systemd/atlax-agent.service /etc/systemd/system/atlax-agent.service
sudo systemctl daemon-reload
```

**Step 7: Switch services.**

```bash
sudo systemctl stop atlax-agent
sudo systemctl start atlax-agent

# Verify locally
sudo systemctl status atlax-agent
journalctl -u atlax-agent -n 20 --no-pager
# Look for: "connected to relay"

# Verify from the relay
curl -s http://localhost:9090/agents    # Run on relay host
```

**Step 8: Test end-to-end.**

```bash
# From the internet, test each service through the relay
# SMB (direct)
smbclient //relay.example.com:18445/SharedDrive -U guest

# HTTP services (through Caddy)
curl -I https://app.example.com
curl -I https://tower.app.example.com
curl https://tower.app.example.com/api/health
```

**Step 9: Clean up.**

After a soak period:

```bash
rm -rf /home/agent-user/atlax/
```

---

## 9. Security Checklist

Verify each item after completing setup.

### File Permissions

- [ ] `/etc/atlax/certs/relay.key` is `640` (owner read-write, group read, no other)
- [ ] `/etc/atlax/certs/agent.key` is `640`
- [ ] `/etc/atlax/certs/` directory is `750`
- [ ] All cert/key files are owned by `root:atlax`
- [ ] Config files (`relay.yaml`, `agent.yaml`) are `640` and owned by `root:atlax`

### TLS Configuration

- [ ] `min_version: "1.3"` set in both relay and agent configs
- [ ] Relay uses `relay-chain.crt`, not bare `relay.crt`
- [ ] Agent uses `agent-chain.crt`, not bare `agent.crt`
- [ ] Agent uses `root-ca.crt` as `ca_file`, not `relay-ca.crt`
- [ ] No self-signed certificates in production (use a proper CA)
- [ ] Certificate expiry monitored (90-day rotation cycle)

### Network

- [ ] Relay admin port (9090) bound to `127.0.0.1` only
- [ ] Customer HTTP ports bound to `127.0.0.1` when behind Caddy
- [ ] Only necessary ports open in firewall and security groups
- [ ] Agent makes only outbound connections (no inbound ports exposed)

### systemd Hardening

- [ ] `NoNewPrivileges=yes` set
- [ ] `ProtectSystem=strict` set
- [ ] `ProtectHome=yes` set
- [ ] `PrivateTmp=yes` set
- [ ] `PrivateDevices=yes` set
- [ ] `MemoryDenyWriteExecute=yes` set
- [ ] `ReadOnlyPaths=/etc/atlax` set
- [ ] Agent unit has `CapabilityBoundingSet=` (empty, no capabilities)
- [ ] Relay unit has only `CAP_NET_BIND_SERVICE` if binding privileged ports

### Operational

- [ ] Service names match between relay `ports[].service` and agent `services[].name`
- [ ] Customer ID matches between relay `customers[].id` and agent cert CN
- [ ] Logs are structured JSON (`format: json`)
- [ ] `Restart=always` set in systemd units
- [ ] `LimitNOFILE` scaled for expected connection count

---

## 10. Troubleshooting

### TLS Handshake Failures

**Symptom:** Agent logs `"TLS handshake failed"` or `"x509: certificate signed by unknown authority"`.

**Causes and fixes:**

| Cause | Fix |
|-------|-----|
| Relay serves bare `relay.crt` instead of `relay-chain.crt` | Set `cert_file` to `relay-chain.crt` in relay config |
| Agent sends bare `agent.crt` instead of `agent-chain.crt` | Set `cert_file` to `agent-chain.crt` in agent config |
| Agent `ca_file` is `relay-ca.crt` instead of `root-ca.crt` | Set `ca_file` to `root-ca.crt` in agent config |
| `server_name` in agent config does not match relay cert SAN | Check relay cert SANs with `openssl x509 -in relay.crt -noout -text | grep -A1 "Subject Alternative Name"` |
| Certificate expired | Check with `openssl x509 -in cert.crt -noout -dates`. Regenerate if expired. |

**Diagnostic commands:**

```bash
# Verify certificate chain on the relay
openssl verify -CAfile /etc/atlax/certs/root-ca.crt \
  -untrusted /etc/atlax/certs/relay-chain.crt \
  /etc/atlax/certs/relay-chain.crt

# Check certificate expiry
openssl x509 -in /etc/atlax/certs/relay-chain.crt -noout -enddate

# Test TLS connection to relay (from agent host)
openssl s_client -connect relay.example.com:8443 \
  -CAfile /etc/atlax/certs/root-ca.crt \
  -cert /etc/atlax/certs/agent-chain.crt \
  -key /etc/atlax/certs/agent.key \
  -servername relay.atlax.local
```

### Agent Cannot Connect

**Symptom:** Agent logs `"cannot connect to relay"` or connection timeouts.

| Cause | Fix |
|-------|-----|
| Port 8443 blocked by egress firewall | Check with `nc -zv relay.example.com 8443` from agent host |
| Port 8443 not open in relay security group | Add inbound rule for TCP 8443 |
| Relay not running | Check `systemctl status atlax-relay` on relay host |
| Wrong relay address in agent config | Verify `relay.addr` matches relay public IP/DNS |

### Unknown Service Errors

**Symptom:** Relay logs `"unknown service: <name>"` when a client connects to a customer port.

**Cause:** The agent's `services[].name` does not match the relay's `customers[].ports[].service`.

**Fix:** Compare the service names in both config files side by side. They must be identical strings (case-sensitive). See [Section 5.5](#55-service-name-matching).

### Customer Not Found

**Symptom:** Relay logs `"customer not found"` during agent mTLS handshake.

**Cause:** The CN in the agent's certificate does not match any `customers[].id` in the relay config.

**Fix:**

```bash
# Check the agent cert CN
openssl x509 -in /etc/atlax/certs/agent-chain.crt -noout -subject
# Output: subject=... CN=customer-dev-001

# Ensure relay.yaml has a matching customer ID
grep "id:" /etc/atlax/relay.yaml
# Must contain: id: "customer-dev-001"
```

### Permission Denied on Key Files

**Symptom:** Service fails to start with `"permission denied"` reading certificate or key files.

**Fix:**

```bash
# Verify ownership and permissions
ls -la /etc/atlax/certs/

# Fix ownership
sudo chown root:atlax /etc/atlax/certs/*

# Fix key permissions (group-readable for the atlax service user)
sudo chmod 640 /etc/atlax/certs/*.key

# Fix cert permissions
sudo chmod 644 /etc/atlax/certs/*.crt
```

### Relay Fails to Bind Customer Port

**Symptom:** Relay logs `"bind: address already in use"` for a customer port.

**Fix:**

```bash
# Find what is using the port
sudo ss -tlnp | grep :18080

# Either stop the conflicting process or change the port in relay.yaml
```

### Agent Frequent Reconnections

**Symptom:** Agent logs show repeated `"disconnected"` / `"connected to relay"` cycles.

| Cause | Fix |
|-------|-----|
| Unstable ISP connection | Check with `ping -c 100 relay.example.com` for packet loss |
| Relay restarting due to crashes | Check relay logs for panics or OOM kills |
| Keepalive timeout too aggressive | Increase `keepalive_timeout` in agent config (e.g., 30s) |
| Idle timeout too short | Increase `idle_timeout` in relay config |

### Caddy Cannot Reach Customer Port

**Symptom:** Caddy returns 502 Bad Gateway.

| Cause | Fix |
|-------|-----|
| Customer port not bound to `127.0.0.1` | Set `listen_addr: "127.0.0.1"` in relay config |
| Agent not connected | Check `curl -s http://localhost:9090/agents` on relay |
| Wrong port in Caddyfile | Verify Caddyfile `reverse_proxy` port matches relay config |
| Relay not listening on the port | Check `ss -tlnp | grep :<port>` on relay |
