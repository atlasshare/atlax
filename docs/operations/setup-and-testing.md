# atlax Setup and Testing Guide

> **Recommended:** Use the [`ats` CLI](https://github.com/atlasshare/atlax-tools) for interactive setup. `ats setup relay` and `ats setup agent` automate most of the steps below (PKI, config generation, systemd, firewall). The manual steps in this guide are for operators who prefer full control or need to understand the internals.

This guide walks through setting up atlax in three stages:

1. **Local testing** -- both binaries on localhost
2. **LAN testing** -- relay on MacBook, agent on Arch (via local network)
3. **Production** -- relay on AWS EC2 with static IP, agent on customer node

Tested and verified on 2026-03-30 with real Samba and web app traffic.

---

## Prerequisites

- Go 1.25+ installed on build machine
- OpenSSL 3.x (for certificate generation), or `ats certs init` from [atlax-tools](https://github.com/atlasshare/atlax-tools)
- SSH access to the agent host
- For Stage 3: AWS EC2 instance with Elastic IP

---

## Stage 1: Local Testing (both on MacBook)

### 1.1 Build the binaries

```bash
cd ~/projects/atlax
make build
```

Verify:

```bash
ls -la bin/atlax-relay bin/atlax-agent
```

### 1.2 Generate development certificates

```bash
make certs-dev
```

Verify the chain:

```bash
openssl verify -CAfile certs/root-ca.crt -untrusted certs/relay-ca.crt certs/relay.crt
openssl verify -CAfile certs/root-ca.crt -untrusted certs/customer-ca.crt certs/agent.crt
```

Both should output `OK`.

### 1.3 Start a local echo server

The agent needs a local service to forward traffic to:

```bash
# Option A: socat
socat TCP-LISTEN:9999,reuseaddr,fork EXEC:cat

# Option B: Python
python3 -c "
import socket, threading
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', 9999)); s.listen()
print('echo server on :9999')
while True:
    c, _ = s.accept()
    threading.Thread(target=lambda c=c: [c.sendall(c.recv(4096)), c.close()]).start()
"
```

### 1.4 Create relay config

Create `relay-local.yaml`:

```yaml
server:
  listen_addr: 0.0.0.0:8443
  max_agents: 10
  max_streams_per_agent: 100
  idle_timeout: 300s
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
        description: Echo test service

logging:
  level: debug
  format: text
```

### 1.5 Create agent config

Create `agent-local.yaml`:

```yaml
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
```

**Important:** The agent's `ca_file` must be `relay-ca.crt` (the Relay Intermediate CA), not `root-ca.crt`.

### 1.6 Start relay

```bash
./bin/atlax-relay -config relay-local.yaml
```

### 1.7 Start agent (new terminal)

```bash
./bin/atlax-agent -config agent-local.yaml
```

You should see `agent: connected to relay` in the agent logs and `relay: agent connected` with `customer_id: customer-dev-001` in the relay logs.

### 1.8 Test the tunnel

```bash
echo "hello atlax" | nc localhost 18080
# Should receive: hello atlax
```

### 1.9 Test graceful shutdown

```bash
kill -TERM $(pgrep atlax-relay)
```

Relay logs GOAWAY and shuts down cleanly. Agent detects the disconnect.

---

## Stage 2: LAN Testing (relay on MacBook, agent on remote host)

### 2.1 Regenerate certificates with relay IP

The default dev certs only have SANs for `localhost` and `127.0.0.1`. For cross-machine testing, add the relay host's IP.

Edit `scripts/gen-certs.sh`, find the relay cert SAN line (around line 103):

```bash
# Change this:
subjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1

# To this (replace with your relay host IP):
subjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1,IP:<RELAY_IP>
```

Regenerate:

```bash
rm -rf certs/
make certs-dev
```

Verify the new SAN:

```bash
openssl x509 -in certs/relay.crt -noout -ext subjectAltName
```

### 2.2 Cross-compile agent for the target platform

```bash
# For Linux amd64:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/atlax-agent-linux ./cmd/agent/
```

### 2.3 Deploy to agent host

```bash
ssh <AGENT_HOST> "mkdir -p ~/atlax/{certs,bin}"

# Copy binary
scp bin/atlax-agent-linux <AGENT_HOST>:~/atlax/bin/atlax-agent
ssh <AGENT_HOST> "chmod +x ~/atlax/bin/atlax-agent"

# Copy ALL agent certs (agent cert+key, relay CA for verification)
scp certs/agent.crt certs/agent.key certs/relay-ca.crt <AGENT_HOST>:~/atlax/certs/
```

### 2.4 Create agent config on remote host

SSH into the agent host and create the config file directly:

```bash
ssh <AGENT_HOST>
cat > ~/atlax/agent.yaml << 'EOF'
relay:
  addr: <RELAY_IP>:8443
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
```

**Do not create config files via SSH heredocs in a single command** (e.g., `ssh host "cat > file << 'EOF' ... EOF"`). The shell quoting corrupts YAML values with escaped quotes.

### 2.5 Start and test

Start the relay on the relay host, start the agent on the agent host, then test:

```bash
echo "hello" | nc <RELAY_IP> 18080
```

---

## Stage 3: Production (relay on AWS, agent on customer node)

### 3.1 Provision AWS EC2

- **AMI:** Ubuntu 24.04 LTS or Amazon Linux 2023
- **Instance type:** t3.micro (sufficient for testing)
- **Elastic IP:** Allocate and associate
- **Security group inbound rules:**
  - TCP 8443 (agent mTLS connections)
  - TCP 18080+ (one per customer service port)
  - TCP 22 (SSH)

### 3.2 Regenerate certificates with AWS Elastic IP

Edit `scripts/gen-certs.sh` SAN line:

```bash
subjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1,IP:<ELASTIC_IP>
```

Regenerate and verify:

```bash
rm -rf certs/
make certs-dev
openssl x509 -in certs/relay.crt -noout -ext subjectAltName
# Must show: IP Address:<ELASTIC_IP>
```

### 3.3 Cross-compile relay

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/atlax-relay-linux ./cmd/relay/
```

### 3.4 Deploy relay to AWS

```bash
ssh <VPS> "mkdir -p ~/atlax/{certs,bin}"

# Binary
scp bin/atlax-relay-linux <VPS>:~/atlax/bin/atlax-relay
ssh <VPS> "chmod +x ~/atlax/bin/atlax-relay"

# ALL relay certs: relay cert+key, root CA, customer CA
scp certs/relay.crt certs/relay.key certs/root-ca.crt certs/customer-ca.crt <VPS>:~/atlax/certs/
```

### 3.5 Create relay config on AWS

SSH into the VPS and create the config:

```bash
ssh <VPS>
cat > ~/atlax/relay.yaml << 'EOF'
server:
  listen_addr: 0.0.0.0:8443
  admin_addr: 127.0.0.1:9090
  max_agents: 100
  max_streams_per_agent: 100
  idle_timeout: 300s
  shutdown_grace_period: 30s

tls:
  cert_file: ./certs/relay.crt
  key_file: ./certs/relay.key
  ca_file: ./certs/root-ca.crt
  client_ca_file: ./certs/customer-ca.crt

customers:
  - id: customer-dev-001
    ports:
      - port: 18080
        service: http
        description: Web app
      - port: 18070
        service: api
        description: API backend

logging:
  level: info
  format: json
EOF
```

### 3.6 Create systemd unit for relay

```bash
sudo tee /etc/systemd/system/atlax-relay.service << 'EOF'
[Unit]
Description=atlax Relay Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=<YOUR_USER>
WorkingDirectory=/home/<YOUR_USER>/atlax
ExecStart=/home/<YOUR_USER>/atlax/bin/atlax-relay -config /home/<YOUR_USER>/atlax/relay.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable atlax-relay
sudo systemctl start atlax-relay
```

Verify:

```bash
sudo systemctl status atlax-relay
sudo journalctl -u atlax-relay -f
```

### 3.7 Deploy agent to customer node

Follow the same steps as Stage 2, but point `relay.addr` to the Elastic IP.

**Critical: when you regenerate certs, deploy ALL files to ALL machines:**

| Machine | Files needed |
|---------|-------------|
| Relay (AWS) | relay.crt, relay.key, root-ca.crt, customer-ca.crt |
| Agent (customer) | agent.crt, agent.key, relay-ca.crt |

Partial certificate deployment causes `tls: unknown certificate authority` errors.

### 3.8 Create systemd unit for agent

On the agent host:

```bash
sudo tee /etc/systemd/system/atlax-agent.service << 'EOF'
[Unit]
Description=atlax Tunnel Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=<YOUR_USER>
WorkingDirectory=/home/<YOUR_USER>/atlax
ExecStart=/home/<YOUR_USER>/atlax/bin/atlax-agent -config /home/<YOUR_USER>/atlax/agent.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable atlax-agent
sudo systemctl start atlax-agent
```

### 3.9 Test from anywhere

```bash
# Web app:
curl http://<ELASTIC_IP>:18080

# API:
curl http://<ELASTIC_IP>:18070/health

# SMB (CLI only -- Finder does not support custom ports):
smbclient -L //<ELASTIC_IP> -p 18445 -N
```

### 3.10 Deploying new binaries

The running binary file is locked by the OS. To deploy an update:

```bash
# 1. Stop the service
ssh <VPS> "sudo systemctl stop atlax-relay"

# 2. Upload new binary
scp bin/atlax-relay-linux <VPS>:~/atlax/bin/atlax-relay

# 3. Start the service
ssh <VPS> "sudo systemctl start atlax-relay"
```

---

## Multi-Service Configuration

To expose multiple services through a single agent, each service needs a unique name that matches between the relay and agent configs.

**Relay config (ports section):**

```yaml
customers:
  - id: customer-dev-001
    ports:
      - port: 18445
        service: smb
      - port: 18080
        service: http
      - port: 18070
        service: api
```

**Agent config (services section):**

```yaml
services:
  - name: smb
    local_addr: 127.0.0.1:445
    protocol: tcp
  - name: http
    local_addr: 127.0.0.1:3009
    protocol: tcp
  - name: api
    local_addr: 127.0.0.1:7070
    protocol: tcp
```

The `service` name in the relay must exactly match the `name` in the agent. The relay sends the service name in the STREAM_OPEN frame payload, and the agent uses it to route to the correct local address.

**If you only have one service**, the agent routes all streams to it regardless of the service name (single-service fallback).

### Migrating Docker services to atlax

If your services are Docker containers bound to a specific IP (e.g., Tailscale), rebind them to `127.0.0.1` so the atlax agent can reach them:

```yaml
# Before (Tailscale-bound):
ports:
  - "100.103.184.98:3009:3009"

# After (localhost-bound, reachable by atlax agent):
ports:
  - "127.0.0.1:3009:3009"
```

For frontend apps with API backends, update the API URL environment variable to point to the relay's public address:

```yaml
environment:
  - VITE_API_URL=http://<ELASTIC_IP>:18070
```

Rebuild the container after changing environment variables that are baked into the frontend build.

---

## Troubleshooting

### Relay fails to start: "unknown port" or "invalid address"

Config has quoted values with escaped characters. Check `relay.yaml` for `\"` -- YAML values should not have escaped quotes. Use unquoted values:

```yaml
# Wrong:
listen_addr: \"0.0.0.0:8443\"

# Correct:
listen_addr: 0.0.0.0:8443
```

### Agent error: "tls: unknown certificate authority"

The relay's `customer-ca.crt` does not match the CA that signed `agent.crt`. This happens after regenerating certificates without deploying all files. Fix: copy ALL cert files from the generation to ALL machines (see Section 3.7 table).

Verify the chain:

```bash
# On agent host:
openssl verify -CAfile ~/atlax/certs/relay-ca.crt ~/atlax/certs/agent.crt

# On relay host:
openssl x509 -in ~/atlax/certs/customer-ca.crt -noout -fingerprint -sha256
# Compare with:
openssl x509 -in ~/atlax/certs/agent.crt -noout -issuer
```

### Agent error: "certificate is not valid for <IP>"

The relay cert does not have the relay's IP in its Subject Alternative Names. Regenerate certs with the IP in the SAN (see Section 3.2).

### Multi-service routing sends to wrong service

All services get the same response, or some services hang. Ensure:
1. Service names match exactly between relay `ports[].service` and agent `services[].name`
2. You are running relay binary version with the port-based routing fix (commit `79a88dd` or later)

### Cannot SCP binary to relay: "Failure"

The binary file is locked by the running process. Stop the service first:

```bash
sudo systemctl stop atlax-relay
```

### macOS Finder / iOS Files: cannot connect to SMB on custom port

Finder and iOS Files app do not support custom SMB ports. Use CLI tools or third-party apps:

```bash
# CLI:
smbclient -L //<RELAY_IP> -p 18445 -N

# Mount:
mkdir -p /tmp/share
mount_smbfs //<USER>@<RELAY_IP>:18445/SharedDrive /tmp/share
```

On iOS, use FE File Explorer or Documents by Readdle.

---

## Security Notes

- **Dev certs are for testing only.** Production certs should be issued by a proper CA (step-ca, Vault PKI, cfssl).
- **Client-facing relay ports accept plain TCP.** Encryption is provided by the mTLS tunnel between relay and agent. The client-to-relay leg is unencrypted.
- **Restrict the agent listener port (8443).** Only agents with valid mTLS certs can connect, but limit source IPs via security group if possible.
- **Protect private keys.** Use `chmod 600` on all `.key` files. Never commit keys to git.
- **Certificate deployment is all-or-nothing.** When regenerating, update every file on every machine from the same generation.
