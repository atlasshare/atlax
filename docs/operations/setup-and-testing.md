# atlax Setup and Testing Guide

This guide walks through setting up atlax in three stages:

1. **Local testing** -- both binaries on localhost
2. **LAN testing** -- relay on MacBook, agent on Arch (via Tailscale)
3. **Production-like** -- relay on AWS with static IP, agent on Arch

## Prerequisites

- Go 1.25+ installed on build machine
- OpenSSL 3.x (for certificate generation)
- Tailscale configured between MacBook (100.65.194.23) and Arch (100.103.184.98)
- SSH access to Arch: `ssh 100.103.184.98`
- An echo server or Samba running on Arch for traffic testing

---

## Stage 1: Local Testing (both on MacBook)

### 1.1 Build the binaries

```bash
cd ~/projects/atlax
make build
# Or manually:
go build -o bin/atlax-relay ./cmd/relay/
go build -o bin/atlax-agent ./cmd/agent/
```

Verify:

```bash
ls -la bin/atlax-relay bin/atlax-agent
```

### 1.2 Generate development certificates

```bash
make certs-dev
```

This creates `certs/` with the full CA hierarchy. Verify:

```bash
openssl verify -CAfile certs/root-ca.crt -untrusted certs/relay-ca.crt certs/relay.crt
openssl verify -CAfile certs/root-ca.crt -untrusted certs/customer-ca.crt certs/agent.crt
# Both should output: OK
```

### 1.3 Start a local echo server (test target)

The agent needs a local service to forward traffic to. Start a simple echo server:

```bash
# Option A: ncat (from nmap)
ncat -l -k -e /bin/cat 127.0.0.1 9999

# Option B: socat
socat TCP-LISTEN:9999,reuseaddr,fork EXEC:cat

# Option C: Go one-liner (save as /tmp/echo.go)
cat > /tmp/echo.go << 'EOF'
package main

import (
    "io"
    "net"
    "log"
)

func main() {
    ln, err := net.Listen("tcp", "127.0.0.1:9999")
    if err != nil { log.Fatal(err) }
    log.Println("echo server listening on :9999")
    for {
        conn, err := ln.Accept()
        if err != nil { continue }
        go func() {
            defer conn.Close()
            io.Copy(conn, conn)
        }()
    }
}
EOF
go run /tmp/echo.go
```

Leave this running in a terminal.

### 1.4 Create relay config

```bash
cat > relay-local.yaml << 'EOF'
server:
  listen_addr: ":8443"
  admin_addr: ":9090"
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
  - id: "customer-dev-001"
    ports:
      - port: 18080
        service: "echo"
        description: "Echo test service"

logging:
  level: debug
  format: text
EOF
```

Port 18080 avoids conflicts with common services. The relay will listen on `:8443` for agents and `:18080` for clients.

### 1.5 Create agent config

```bash
cat > agent-local.yaml << 'EOF'
relay:
  addr: "127.0.0.1:8443"
  server_name: "relay.atlax.local"
  keepalive_interval: 10s
  keepalive_timeout: 5s

tls:
  cert_file: ./certs/agent.crt
  key_file: ./certs/agent.key
  ca_file: ./certs/relay-ca.crt

services:
  - name: "echo"
    local_addr: "127.0.0.1:9999"
    protocol: "tcp"

logging:
  level: debug
  format: text
EOF
```

### 1.6 Start relay

```bash
./bin/atlax-relay -config relay-local.yaml
```

Expected output (debug level):

```
{"level":"INFO","msg":"relay: agent listener started","addr":"[::]:8443"}
{"level":"INFO","msg":"relay: client listener started","addr":"[::]:18080","port":18080}
{"level":"INFO","msg":"relay started","listen_addr":":8443","customers":1,"ports":1}
```

### 1.7 Start agent (new terminal)

```bash
./bin/atlax-agent -config agent-local.yaml
```

Expected output:

```
{"level":"INFO","msg":"agent: connected to relay","addr":"127.0.0.1:8443"}
{"level":"INFO","msg":"agent started","relay":"127.0.0.1:8443","services":1}
```

On the relay side, you should see:

```
{"level":"INFO","msg":"relay: agent connected","customer_id":"customer-dev-001",...}
```

### 1.8 Test the tunnel

```bash
# In a new terminal:
echo "hello atlax" | nc localhost 18080
# Should receive: hello atlax

# Interactive test:
nc localhost 18080
# Type anything, press Enter. You should see it echoed back.
# Press Ctrl+C to disconnect.
```

### 1.9 Verify graceful shutdown

```bash
# Kill the relay with SIGTERM:
kill -TERM $(pgrep atlax-relay)
# Relay should log GOAWAY and clean shutdown.
# Agent should detect the disconnect.
```

---

## Stage 2: LAN Testing (Relay on MacBook, Agent on Arch via Tailscale)

### 2.1 Regenerate certificates with Tailscale IP

The default dev certs only have SANs for `localhost` and `127.0.0.1`. For cross-machine testing, the relay cert needs the MacBook's Tailscale IP.

Edit `scripts/gen-certs.sh` and find the relay cert generation line (around line 103):

```bash
# Find this line:
subjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1

# Change to:
subjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1,IP:100.65.194.23
```

Then regenerate:

```bash
rm -rf certs/
make certs-dev
```

Verify the new cert has the Tailscale IP:

```bash
openssl x509 -in certs/relay.crt -noout -ext subjectAltName
# Should show: IP Address:100.65.194.23
```

### 2.2 Cross-compile agent for Linux

```bash
GOOS=linux GOARCH=amd64 go build -o bin/atlax-agent-linux ./cmd/agent/
```

### 2.3 Copy files to Arch

```bash
# Create directory on Arch
ssh 100.103.184.98 "mkdir -p ~/atlax/{certs,bin}"

# Copy binary
scp bin/atlax-agent-linux 100.103.184.98:~/atlax/bin/atlax-agent
ssh 100.103.184.98 "chmod +x ~/atlax/bin/atlax-agent"

# Copy certificates (agent needs: agent cert+key, relay CA for verification)
scp certs/agent.crt certs/agent.key certs/relay-ca.crt 100.103.184.98:~/atlax/certs/
```

### 2.4 Create agent config on Arch

SSH into Arch and create the file directly:

```bash
ssh 100.103.184.98
cat > ~/atlax/agent.yaml << 'EOF'
relay:
  addr: 100.65.194.23:8443
  server_name: relay.atlax.local
  keepalive_interval: 10s
  keepalive_timeout: 5s

tls:
  cert_file: ./certs/agent.crt
  key_file: ./certs/agent.key
  ca_file: ./certs/relay-ca.crt

services:
  - name: smb
    local_addr: 127.0.0.1:445
    protocol: tcp

logging:
  level: debug
  format: text
EOF
```

This forwards Samba (port 445) from the Arch box through the tunnel.

### 2.5 Start relay on MacBook

```bash
cat > relay-tailscale.yaml << 'EOF'
server:
  listen_addr: "0.0.0.0:8443"
  admin_addr: ":9090"
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
  - id: "customer-dev-001"
    ports:
      - port: 18445
        service: "smb"
        description: "Samba via tunnel"

logging:
  level: debug
  format: text
EOF

./bin/atlax-relay -config relay-tailscale.yaml
```

### 2.6 Start agent on Arch

```bash
ssh 100.103.184.98
cd ~/atlax
./bin/atlax-agent -config agent.yaml
```

### 2.7 Test SMB through the tunnel

From the MacBook:

```bash
# Test raw TCP connectivity first:
nc -zv localhost 18445
# Should show: Connection to localhost port 18445 [tcp/*] succeeded!

# Test SMB (if smbclient is installed):
smbclient -L //127.0.0.1 -p 18445 -N
# Should list the Samba shares from the Arch box

# Or mount (macOS):
# open smb://127.0.0.1:18445/SharedDrive
```

If Samba is not running on Arch, start an echo server instead:

```bash
# On Arch:
ncat -l -k -e /bin/cat 127.0.0.1 9999

# Update agent.yaml to point to 127.0.0.1:9999 instead of :445
# Update relay config to use service: "echo" instead of "smb"
```

### 2.8 Verify cross-machine tunnel works

```bash
echo "hello from macbook through tailscale tunnel" | nc localhost 18445
# Should echo back the message (if using echo server)
```

---

## Stage 3: AWS Production-Like (Relay on AWS, Agent on Arch)

### 3.1 Provision AWS EC2 instance

- **AMI:** Ubuntu 24.04 LTS (or Amazon Linux 2023)
- **Instance type:** t3.micro (sufficient for testing)
- **Security group:** Allow inbound TCP on ports 8443 (agents) and 18445 (clients)
- **Elastic IP:** Allocate and associate (you need a static IP)
- **Key pair:** Use your existing SSH key

Note the public IP (e.g., `54.x.x.x`).

### 3.2 Regenerate certificates with AWS IP

Edit `scripts/gen-certs.sh`, update the relay SAN line:

```bash
subjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1,IP:100.65.194.23,IP:54.x.x.x
```

Replace `54.x.x.x` with your actual Elastic IP. Regenerate:

```bash
rm -rf certs/
make certs-dev
```

### 3.3 Cross-compile relay for Linux

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/atlax-relay-linux ./cmd/relay/
```

### 3.4 Deploy relay to AWS

```bash
# Create directory
ssh -i ~/.ssh/your-key.pem ubuntu@54.x.x.x "mkdir -p ~/atlax/{certs,bin}"

# Copy binary
scp -i ~/.ssh/your-key.pem bin/atlax-relay-linux ubuntu@54.x.x.x:~/atlax/bin/atlax-relay
ssh -i ~/.ssh/your-key.pem ubuntu@54.x.x.x "chmod +x ~/atlax/bin/atlax-relay"

# Copy certificates (relay needs: relay cert+key, root CA, customer CA)
scp -i ~/.ssh/your-key.pem \
  certs/relay.crt certs/relay.key \
  certs/root-ca.crt certs/customer-ca.crt \
  ubuntu@54.x.x.x:~/atlax/certs/
```

### 3.5 Create relay config on AWS

SSH into the instance and create the file directly:

```bash
ssh -i ~/.ssh/your-key.pem ubuntu@54.x.x.x
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
      - port: 18445
        service: smb
        description: Samba via tunnel

logging:
  level: info
  format: json
EOF
```

### 3.6 Start relay on AWS (with systemd)

Create a systemd unit for persistence:

```bash
ssh -i ~/.ssh/your-key.pem ubuntu@54.x.x.x "sudo tee /etc/systemd/system/atlax-relay.service << 'SVCEOF'
[Unit]
Description=atlax Relay Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=ubuntu
WorkingDirectory=/home/ubuntu/atlax
ExecStart=/home/ubuntu/atlax/bin/atlax-relay -config /home/ubuntu/atlax/relay.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SVCEOF"

ssh -i ~/.ssh/your-key.pem ubuntu@54.x.x.x "sudo systemctl daemon-reload && sudo systemctl enable atlax-relay && sudo systemctl start atlax-relay"
```

Verify:

```bash
ssh -i ~/.ssh/your-key.pem ubuntu@54.x.x.x "sudo systemctl status atlax-relay"
ssh -i ~/.ssh/your-key.pem ubuntu@54.x.x.x "sudo journalctl -u atlax-relay -f"
```

### 3.7 Update agent on Arch to point to AWS

SSH into Arch and create the file:

```bash
ssh 100.103.184.98
cat > ~/atlax/agent.yaml << 'EOF'
relay:
  addr: 54.x.x.x:8443
  server_name: relay.atlax.local
  keepalive_interval: 30s
  keepalive_timeout: 10s

tls:
  cert_file: ./certs/agent.crt
  key_file: ./certs/agent.key
  ca_file: ./certs/relay-ca.crt

services:
  - name: smb
    local_addr: 127.0.0.1:445
    protocol: tcp

logging:
  level: info
  format: json
EOF
```

Also copy the updated relay-ca.crt (in case it changed during cert regen):

```bash
scp certs/relay-ca.crt 100.103.184.98:~/atlax/certs/
```

### 3.8 Start agent on Arch (with systemd)

```bash
ssh 100.103.184.98 "sudo tee /etc/systemd/system/atlax-agent.service << 'SVCEOF'
[Unit]
Description=atlax Tunnel Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$(whoami)
WorkingDirectory=/home/$(whoami)/atlax
ExecStart=/home/$(whoami)/atlax/bin/atlax-agent -config /home/$(whoami)/atlax/agent.yaml
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SVCEOF"

ssh 100.103.184.98 "sudo systemctl daemon-reload && sudo systemctl enable atlax-agent && sudo systemctl start atlax-agent"
```

### 3.9 Test from anywhere

From any machine that can reach the AWS public IP:

```bash
# SMB through the tunnel:
smbclient -L //54.x.x.x -p 18445 -N

# Or raw TCP echo test:
echo "hello from the internet" | nc 54.x.x.x 18445
```

The traffic path: your machine -> AWS relay (port 18445) -> mTLS tunnel -> Arch agent -> Samba (127.0.0.1:445).

---

## Troubleshooting

### Agent cannot connect to relay

```bash
# Check relay is listening:
ss -tlnp | grep 8443

# Check TLS handshake manually:
openssl s_client -connect <relay-ip>:8443 \
  -cert certs/agent.crt -key certs/agent.key \
  -CAfile certs/relay-ca.crt \
  -servername relay.atlax.local
```

### Certificate errors

```
tls: certificate is not valid for relay.atlax.local
```

The agent's `server_name` must match a SAN in the relay cert. Either update `server_name` in agent.yaml or regenerate certs with the correct SAN.

```
tls: unknown certificate authority
```

The agent's `ca_file` must be the Relay Intermediate CA (`relay-ca.crt`), not the root CA. The relay's `client_ca_file` must be the Customer Intermediate CA (`customer-ca.crt`).

### Relay shows "agent not found" on client connect

The agent has not connected yet, or the customer ID in the cert (`customer-dev-001`) does not match the `id` in the relay's `customers` config.

### Traffic does not flow after connection

Check the service name matches: the relay's port config `service: "smb"` must match the agent's `services[].name: "smb"`. If they do not match and only one service is configured, the single-service fallback will route correctly. With multiple services, the names must match.

### Firewall on Arch blocks local service

```bash
# Check if Samba is listening:
ss -tlnp | grep 445

# If iptables blocks localhost:
sudo iptables -I INPUT -i lo -j ACCEPT
```

---

## Security Notes

- **Dev certs are for testing only.** Do not use them in production. Production certs should be issued by a proper internal CA (step-ca, Vault PKI, cfssl).
- **The relay's client-facing ports (18445) accept plain TCP.** Encryption is provided by the mTLS tunnel between relay and agent. The client-to-relay leg is unencrypted.
- **Do not expose the agent listen port (8443) to the public internet without firewall rules.** Only agents with valid mTLS certificates can connect, but the port should still be restricted to known IP ranges if possible.
- **Certificate private keys must be protected.** Use `chmod 600` on all `.key` files. Never commit keys to git.
