# Deployment Guide

This guide covers deploying the `atlax-relay` and `atlax-agent` binaries in production environments using Docker, systemd, Terraform, and manual installation.

---

## Prerequisites

- Go 1.23+ (for building from source)
- Docker 24+ (for container deployments)
- A public VPS with a static IPv4 address (for the relay)
- Valid mTLS certificates (see [Certificate Operations](certificate-ops.md))
- DNS record pointing to the relay VPS IP

---

## Environment Variables

Both binaries are configured through YAML files with environment variable overrides. Environment variables take precedence over config file values.

### Relay Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ATLAX_RELAY_LISTEN_ADDR` | TLS listen address for agent connections | `:8443` |
| `ATLAX_RELAY_CERT_FILE` | Path to relay TLS certificate | `/etc/atlax/relay.crt` |
| `ATLAX_RELAY_KEY_FILE` | Path to relay TLS private key | `/etc/atlax/relay.key` |
| `ATLAX_RELAY_CA_FILE` | Path to customer CA bundle for mTLS | `/etc/atlax/customer-ca.crt` |
| `ATLAX_RELAY_METRICS_ADDR` | Prometheus metrics listen address | `:9090` |
| `ATLAX_RELAY_HEALTH_ADDR` | Health check HTTP listen address | `:8080` |
| `ATLAX_RELAY_LOG_LEVEL` | Log level (debug, info, warn, error) | `info` |
| `ATLAX_RELAY_LOG_FORMAT` | Log format (json, text) | `json` |
| `ATLAX_RELAY_MAX_AGENTS` | Maximum concurrent agent connections | `1000` |
| `ATLAX_RELAY_MAX_STREAMS_PER_AGENT` | Maximum streams per agent | `100` |
| `ATLAX_RELAY_IDLE_TIMEOUT` | Idle connection timeout | `5m` |

### Agent Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ATLAX_AGENT_RELAY_ADDR` | Relay server address (host:port) | required |
| `ATLAX_AGENT_CERT_FILE` | Path to agent mTLS certificate | `/etc/atlax/agent.crt` |
| `ATLAX_AGENT_KEY_FILE` | Path to agent mTLS private key | `/etc/atlax/agent.key` |
| `ATLAX_AGENT_CA_FILE` | Path to relay CA for verification | `/etc/atlax/relay-ca.crt` |
| `ATLAX_AGENT_LOG_LEVEL` | Log level (debug, info, warn, error) | `info` |
| `ATLAX_AGENT_LOG_FORMAT` | Log format (json, text) | `json` |
| `ATLAX_AGENT_RECONNECT_BACKOFF_MAX` | Maximum reconnection backoff | `60s` |
| `ATLAX_AGENT_HEARTBEAT_INTERVAL` | Heartbeat interval | `30s` |
| `ATLAX_AGENT_UPDATE_CHECK_INTERVAL` | Update check interval | `6h` |
| `ATLAX_AGENT_LOCAL_SERVICES` | Comma-separated local service mappings | required |

---

## Docker Deployment

### Building Images

```bash
# Build relay image
docker build -f deployments/docker/Dockerfile.relay -t atlax-relay:latest .

# Build agent image
docker build -f deployments/docker/Dockerfile.agent -t atlax-agent:latest .
```

### Running the Relay

```bash
docker run -d \
  --name atlax-relay \
  --restart unless-stopped \
  -p 8443:8443 \
  -p 9090:9090 \
  -p 8080:8080 \
  -p 10001-10100:10001-10100 \
  -v /etc/atlax/certs:/etc/atlax:ro \
  -v /var/log/atlax:/var/log/atlax \
  -e ATLAX_RELAY_LOG_LEVEL=info \
  atlax-relay:latest
```

The customer port range (`10001-10100` in this example) must match the range configured for your customer base. Adjust based on the number of customers and ports per customer.

### Running the Agent

```bash
docker run -d \
  --name atlax-agent \
  --restart unless-stopped \
  --network host \
  -v /etc/atlax/certs:/etc/atlax:ro \
  -e ATLAX_AGENT_RELAY_ADDR=relay.example.com:8443 \
  -e ATLAX_AGENT_LOCAL_SERVICES=smb:127.0.0.1:445,http:127.0.0.1:8080 \
  atlax-agent:latest
```

The agent uses `--network host` to access local services running on the customer node.

### Docker Compose

```yaml
# docker-compose.yml (relay)
services:
  relay:
    image: atlax-relay:latest
    restart: unless-stopped
    ports:
      - "8443:8443"
      - "9090:9090"
      - "8080:8080"
      - "10001-10100:10001-10100"
    volumes:
      - /etc/atlax/certs:/etc/atlax:ro
      - /var/log/atlax:/var/log/atlax
    environment:
      ATLAX_RELAY_LOG_LEVEL: info
      ATLAX_RELAY_LOG_FORMAT: json
    deploy:
      resources:
        limits:
          memory: 4G
```

---

## Systemd Deployment

### Relay Service

Place the binary at `/usr/local/bin/atlax-relay` and create the service unit:

```ini
# /etc/systemd/system/atlax-relay.service
[Unit]
Description=atlax Relay Server
Documentation=https://github.com/atlasshare/atlax
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=atlax
Group=atlax
ExecStart=/usr/local/bin/atlax-relay --config /etc/atlax/relay.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
EnvironmentFile=-/etc/atlax/relay.env

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/atlax
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

### Agent Service

```ini
# /etc/systemd/system/atlax-agent.service
[Unit]
Description=atlax Tunnel Agent
Documentation=https://github.com/atlasshare/atlax
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=atlax
Group=atlax
ExecStart=/usr/local/bin/atlax-agent --config /etc/atlax/agent.yaml
Restart=on-failure
RestartSec=5
EnvironmentFile=-/etc/atlax/agent.env

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

### Enabling and Starting

```bash
# Create system user
sudo useradd --system --no-create-home --shell /usr/sbin/nologin atlax

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable atlax-relay
sudo systemctl start atlax-relay

# Check status
sudo systemctl status atlax-relay
sudo journalctl -u atlax-relay -f
```

---

## Terraform Deployment

The `deployments/terraform/` directory contains modules for provisioning relay infrastructure on common cloud providers.

### Resources Provisioned

- VPS instance with a public static IP
- Firewall rules (see Firewall section below)
- DNS A record pointing to the VPS
- TLS certificate deployment
- Systemd service installation

### Usage

```hcl
module "atlax_relay" {
  source = "./deployments/terraform/relay"

  instance_type  = "s-2vcpu-4gb"
  region         = "nyc1"
  domain         = "relay.example.com"
  ssh_key_ids    = [digitalocean_ssh_key.deploy.id]
  cert_path      = "/path/to/relay.crt"
  key_path       = "/path/to/relay.key"
  ca_path        = "/path/to/customer-ca.crt"
  customer_ports = "10001-10100"
}
```

---

## Firewall Configuration

### Relay (Public VPS)

The relay requires carefully scoped inbound rules. Only the agent listener, customer service ports, and operational endpoints should be exposed.

```bash
# iptables example

# Allow agent TLS connections
iptables -A INPUT -p tcp --dport 8443 -j ACCEPT

# Allow customer service port range
iptables -A INPUT -p tcp --dport 10001:10100 -j ACCEPT

# Allow HTTPS (443) if terminating public TLS
iptables -A INPUT -p tcp --dport 443 -j ACCEPT

# Allow health check and metrics from internal monitoring only
iptables -A INPUT -p tcp --dport 8080 -s 10.0.0.0/8 -j ACCEPT
iptables -A INPUT -p tcp --dport 9090 -s 10.0.0.0/8 -j ACCEPT

# Allow SSH from management network
iptables -A INPUT -p tcp --dport 22 -s 10.0.0.0/8 -j ACCEPT

# Drop everything else
iptables -A INPUT -j DROP
```

**UFW equivalent:**

```bash
ufw default deny incoming
ufw default allow outgoing
ufw allow 8443/tcp                    # Agent TLS
ufw allow 443/tcp                     # Public HTTPS (if used)
ufw allow 10001:10100/tcp             # Customer service ports
ufw allow from 10.0.0.0/8 to any port 8080  # Health checks
ufw allow from 10.0.0.0/8 to any port 9090  # Metrics
ufw allow from 10.0.0.0/8 to any port 22    # SSH
ufw enable
```

### Agent (Customer Node)

The agent requires no inbound ports. All connectivity is outbound.

```bash
# Agent firewall: outbound only
# Allow outbound to relay
iptables -A OUTPUT -p tcp --dport 8443 -j ACCEPT

# Allow outbound HTTPS for updates and cert renewal
iptables -A OUTPUT -p tcp --dport 443 -j ACCEPT

# Allow outbound DNS
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
iptables -A OUTPUT -p tcp --dport 53 -j ACCEPT

# Allow local loopback (for forwarding to local services)
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A INPUT -i lo -j ACCEPT
```

---

## Health Checks

Both binaries expose HTTP health check endpoints for load balancer integration and monitoring.

### Relay Health Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/healthz` | GET | Liveness check. Returns 200 if the process is running. |
| `/readyz` | GET | Readiness check. Returns 200 if the relay is accepting agent connections and has loaded certificates. Returns 503 during startup or certificate reload. |
| `/metrics` | GET | Prometheus metrics endpoint. |

### Agent Health Endpoints

The agent exposes a local health endpoint (disabled by default, enable via config) for monitoring the connection status to the relay.

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/healthz` | GET | Returns 200 if the agent process is running. |
| `/readyz` | GET | Returns 200 if the agent has an active TLS connection to the relay. Returns 503 if disconnected or reconnecting. |

### Load Balancer Integration

For active-active relay deployments behind a TCP load balancer:

- Configure the load balancer to probe `/healthz` on the health port (default 8080)
- Use `/readyz` for more conservative checks that exclude relays during certificate rotation
- Set probe interval to 10 seconds with a 3-failure threshold

---

## Resource Sizing

### Relay

| Agents | Streams (total) | CPU | Memory | File Descriptors |
|--------|-----------------|-----|--------|------------------|
| 100 | 10,000 | 2 vCPU | 1 GB | 32,768 |
| 500 | 50,000 | 4 vCPU | 2 GB | 65,536 |
| 1,000 | 100,000 | 8 vCPU | 4 GB | 131,072 |

### Agent

The agent is lightweight. A single agent typically requires less than 50 MB of memory and negligible CPU.

---

## Post-Deployment Verification

After deploying, verify the installation:

```bash
# Check relay health
curl -s http://localhost:8080/healthz

# Check relay readiness
curl -s http://localhost:8080/readyz

# Check metrics are being exported
curl -s http://localhost:9090/metrics | head -20

# Verify TLS listener is accepting connections
openssl s_client -connect relay.example.com:8443 \
  -cert /etc/atlax/test-agent.crt \
  -key /etc/atlax/test-agent.key \
  -CAfile /etc/atlax/relay-ca.crt

# Check systemd service status
systemctl status atlax-relay
journalctl -u atlax-relay --since "5 minutes ago"
```
