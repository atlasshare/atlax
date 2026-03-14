# Getting Started

This guide walks through setting up a local development environment, building atlax from source, generating development certificates, and running your first tunnel test.

---

## Prerequisites

| Tool | Minimum Version | Purpose |
|------|----------------|---------|
| Go | 1.23+ | Compiling the relay and agent binaries |
| Docker | 24+ | Running containerized builds and integration tests |
| make | 3.81+ | Build automation |
| openssl | 1.1+ | Generating development certificates |
| git | 2.30+ | Source control |

### Verify Prerequisites

```bash
go version        # go1.23 or higher
docker --version  # Docker 24 or higher
make --version    # GNU Make 3.81 or higher
openssl version   # OpenSSL 1.1 or higher
git --version     # git 2.30 or higher
```

### Optional Tools

| Tool | Purpose |
|------|---------|
| `golangci-lint` | Running linters locally (also runs in CI) |
| `toxiproxy` | Network fault injection for integration tests |
| `k6` | Load testing |
| `jq` | Parsing JSON output from health and metrics endpoints |

---

## Clone and Build

```bash
# Clone the repository
git clone https://github.com/atlasshare/atlax.git
cd atlax

# Download dependencies
go mod download

# Build both binaries
make build
```

This produces two binaries:

- `bin/atlax-relay` -- the relay server
- `bin/atlax-agent` -- the tunnel agent

### Build Targets

```bash
make build       # Build both binaries
make test        # Run tests with -race flag
make lint        # Run golangci-lint
make clean       # Remove build artifacts
```

---

## Generate Development Certificates

The `scripts/gen-certs.sh` script creates a complete certificate hierarchy for local development:

```bash
# Generate all development certificates
./scripts/gen-certs.sh
```

This creates the following files in `.certs/`:

```
.certs/
  root-ca.crt                      Root CA certificate
  root-ca.key                      Root CA private key
  relay-intermediate-ca.crt        Relay intermediate CA
  relay-intermediate-ca.key        Relay intermediate CA key
  customer-intermediate-ca.crt     Customer intermediate CA
  customer-intermediate-ca.key     Customer intermediate CA key
  relay.crt                        Relay server certificate
  relay.key                        Relay server private key
  customer-dev.crt                 Development agent certificate
  customer-dev.key                 Development agent private key
```

The development agent certificate has `CN=customer-dev` and is signed by the Customer Intermediate CA. The relay certificate has `CN=relay.atlasshare.io` with a SAN for `localhost`.

### Verify Certificates

```bash
# Verify relay certificate chain
openssl verify -CAfile .certs/root-ca.crt \
  -untrusted .certs/relay-intermediate-ca.crt \
  .certs/relay.crt

# Verify agent certificate chain
openssl verify -CAfile .certs/root-ca.crt \
  -untrusted .certs/customer-intermediate-ca.crt \
  .certs/customer-dev.crt

# Inspect a certificate
openssl x509 -in .certs/relay.crt -noout -text
```

---

## Run Locally

### Start the Relay

```bash
bin/atlax-relay \
  --listen-addr :8443 \
  --cert-file .certs/relay.crt \
  --key-file .certs/relay.key \
  --ca-file .certs/customer-intermediate-ca.crt \
  --metrics-addr :9090 \
  --health-addr :8080 \
  --log-level debug
```

The relay will start listening for agent TLS connections on port 8443.

### Start the Agent

In a separate terminal:

```bash
bin/atlax-agent \
  --relay-addr localhost:8443 \
  --cert-file .certs/customer-dev.crt \
  --key-file .certs/customer-dev.key \
  --ca-file .certs/relay-intermediate-ca.crt \
  --local-services smb:127.0.0.1:445,http:127.0.0.1:8080 \
  --log-level debug
```

The agent will dial out to the relay, complete the mTLS handshake, and register itself.

### Verify Connection

```bash
# Check relay health
curl -s http://localhost:8080/healthz
# Expected: {"status":"ok"}

# Check relay readiness
curl -s http://localhost:8080/readyz
# Expected: {"status":"ready"}

# Check that an agent is connected
curl -s http://localhost:9090/metrics | grep atlax_relay_agents_connected
# Expected: atlax_relay_agents_connected 1
```

---

## First Tunnel Test

This test verifies end-to-end traffic flow through the tunnel.

### Step 1: Start a Local Service

Start a simple HTTP server on the agent node to simulate a local service:

```bash
# Simple HTTP server on port 8081
python3 -m http.server 8081
```

### Step 2: Configure the Agent

Start the agent pointing to the local HTTP server:

```bash
bin/atlax-agent \
  --relay-addr localhost:8443 \
  --cert-file .certs/customer-dev.crt \
  --key-file .certs/customer-dev.key \
  --ca-file .certs/relay-intermediate-ca.crt \
  --local-services http:127.0.0.1:8081 \
  --log-level debug
```

### Step 3: Connect Through the Relay

Once the relay assigns a customer port (visible in relay logs), connect to that port:

```bash
# The relay log will show the assigned port, e.g., 10001
curl http://localhost:10001/
```

You should see the directory listing from the Python HTTP server, routed through the relay and tunnel.

### Step 4: Verify in Logs

Check the relay logs for:
- `agent connected` with `customer_id=customer-dev`
- `stream opened` when the curl request arrives
- `stream closed` after the response completes

Check the agent logs for:
- `connected to relay`
- `stream received` with the target service
- `forwarding to local service`

---

## Troubleshooting

### TLS Handshake Failure

**Error:** `tls: certificate required` or `tls: bad certificate`

- Verify the agent certificate is signed by the CA the relay trusts (Customer Intermediate CA)
- Verify the relay certificate is signed by the CA the agent trusts (Relay Intermediate CA)
- Check that certificate files are readable by the process
- Regenerate certificates with `./scripts/gen-certs.sh`

### Connection Refused

**Error:** `dial tcp localhost:8443: connection refused`

- Verify the relay is running: `curl http://localhost:8080/healthz`
- Check if the relay port is in use by another process: `lsof -i :8443`
- Verify the relay started without errors in the log output

### Agent Keeps Reconnecting

**Symptom:** Agent logs show repeated `connecting to relay` messages

- Check the relay log for handshake errors (the relay side will show the rejection reason)
- Verify the agent's `--relay-addr` matches the relay's `--listen-addr`
- Ensure the CA files are correct on both sides

### Port Already in Use

**Error:** `bind: address already in use`

- Another process is using the port. Find it with: `lsof -i :<port>`
- Change the relay or agent listen port via configuration
- If a previous instance is still running: `kill $(pidof atlax-relay)`

### Certificate Verification Failed

**Error:** `x509: certificate signed by unknown authority`

- The CA file does not include the intermediate CA that signed the peer certificate
- For the relay, `--ca-file` must point to the Customer Intermediate CA certificate
- For the agent, `--ca-file` must point to the Relay Intermediate CA certificate
- Verify the chain: `openssl verify -CAfile <ca-file> <cert-file>`

---

## Next Steps

- Read the [Contributing Guide](contributing.md) for development workflow and code conventions
- Read the [Testing Strategy](testing.md) for how to write and run tests
- Read the [Architecture Decision Records](architecture-decisions.md) for design rationale
- Review the [Wire Protocol Specification](../protocol/) for protocol details
