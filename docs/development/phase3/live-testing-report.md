# Phase 3 Live Testing Report

**Date:** 2026-03-30
**Environment:** MacBook (dev) -> AWS EC2 t3.micro us-east-1 (relay) -> Arch Linux (agent)
**Relay IP:** 18.207.237.252
**Agent host:** 192.168.1.66 (LAN) / 100.103.184.98 (Tailscale)
**Services tested:** Samba (445), Dashboard web (3009), Dashboard API (7070)

---

## Test Results

| Test | Result | Notes |
|------|--------|-------|
| Relay starts on AWS with systemd | PASS (after config fix) | Initial failure from escaped YAML quotes |
| Agent connects to relay over mTLS | PASS (after cert sync) | Required all certs regenerated and synced |
| Samba tunneled (port 18445) | PASS | `smbclient -L` lists all 3 shares |
| Dashboard web tunneled (port 18080) | PASS | Full HTML served correctly |
| Dashboard API tunneled (port 18070) | PASS (after routing fix) | Health endpoint returns JSON |
| Multi-service routing | PASS (after code fix) | 3 services on 3 ports, correct routing |
| iPhone SMB via Files app | FAIL | iOS Files app does not support custom SMB ports |
| macOS Finder SMB | FAIL | Finder ignores port in smb:// URL |
| macOS smbclient CLI | PASS | Custom port works with `-p` flag |
| mount_smbfs with custom port | NOT TESTED | Should work |
| Graceful shutdown (SIGTERM relay) | PASS | Agent detects disconnect |
| Agent reconnection after relay restart | PASS | systemd Restart=always recovers agent |

---

## Bugs Found and Fixed

### 1. YAML heredocs with escaped quotes (config parsing failure)

**Symptom:** Relay stuck in restart loop. Error: `lookup tcp/8443\"`: unknown port`

**Root cause:** The setup guide used SSH heredocs with escaped quotes (`\"value\"`). When the heredoc is inside an SSH command with double-quoted outer shell, the `\"` becomes a literal `"` character in the YAML value. Go's `net.Listen` receives `"0.0.0.0:8443"` (with quotes) and fails to parse.

**Fix:** Rewrote all config creation instructions to SSH in first, then create files with local heredocs using unquoted YAML values. YAML does not require quotes around simple string values.

**Impact:** All three stages of the setup guide were affected.

### 2. Certificate mismatch after regeneration (mTLS handshake failure)

**Symptom:** Agent connects but relay rejects with `tls: unknown certificate authority`.

**Root cause:** After regenerating certs (to add AWS IP to relay SAN), only some cert files were copied to remote machines. The agent on Arch had an old `agent.crt` signed by the previous customer CA, but the relay on AWS had the new `customer-ca.crt`. The CA fingerprints did not match.

**Fix:** Copied ALL regenerated cert files to ALL machines. Rule: when you regenerate certs, you must update every file on every machine that uses any cert from that generation.

**Lesson:** Certificate deployment is all-or-nothing per generation. Partial updates cause cross-CA mismatches that are hard to diagnose.

### 3. Multi-service routing broken (PortRouter.Route used wrong service)

**Symptom:** Port 18080 (web) returned API's 404 response. Port 18070 (API) hung with no response.

**Root cause:** `PortRouter.Route` received `customerID` but not the port the client connected on. It iterated the port map looking for the first entry matching the customer ID. With multiple services per customer, Go map iteration order is non-deterministic, so the wrong service was selected randomly.

**Fix:** Added `port int` parameter to `Route` and `TrafficRouter` interface. Route now looks up the exact port entry instead of scanning by customer ID.

**Impact:** This was a code bug, not a config issue. Fixed in commit `79a88dd`. All multi-service deployments were affected.

### 4. Docker services bound to Tailscale IP (agent cannot reach them)

**Symptom:** Agent config pointed to `127.0.0.1:3009` but the Docker container was bound to `100.103.184.98:3009` (Tailscale IP only).

**Root cause:** The dashboard Docker Compose file used `100.103.184.98:PORT:PORT` port mappings, which only accepts connections on the Tailscale interface. The atlax agent connects to `127.0.0.1`, which is a different interface.

**Fix:** Changed Docker Compose port bindings to `127.0.0.1:PORT:PORT`. Also updated `VITE_API_URL` to point to the relay's public URL so the browser can reach the API through the tunnel.

**Lesson:** When migrating from Tailscale to atlax, all services must be rebound from the Tailscale IP to `127.0.0.1` (or `0.0.0.0` if local access is also needed).

---

## Operational Observations

### Systemd integration

- `Restart=always` with `RestartSec=5` provides automatic recovery for both relay and agent
- The ops user (`atlax-relay-op`) requires sudo for systemctl -- passwordless sudo for `systemctl restart atlax-relay` would simplify operations
- Cannot SCP to a running binary (file is locked) -- must stop the service first before deploying new binaries

### Certificate management

- Dev certs work for testing but the 90-day validity means they expire silently
- When adding a new relay IP (e.g., migrating to a new VPS), all certs must be regenerated and redeployed to all machines
- The agent uses `relay-ca.crt` (intermediate CA), NOT `root-ca.crt`, for server verification
- The relay uses `customer-ca.crt` (intermediate CA) for client verification

### SMB over custom ports

- macOS Finder and iOS Files app do not support custom SMB ports
- `smbclient -p PORT` works from CLI
- `mount_smbfs //user@host:port/share /mountpoint` should work for macOS mounting
- Third-party apps (FE File Explorer) support custom ports on iOS

### Multi-service deployment

- Each service needs a unique name matching between relay config (`ports[].service`) and agent config (`services[].name`)
- The STREAM_OPEN payload carries the service name as raw UTF-8
- Single-service fallback only works when exactly one service is configured
- Frontend apps with API backends need `VITE_API_URL` (or equivalent) pointed at the relay URL, not localhost

---

## Recommendations for Production

1. **Automate cert deployment** -- A single script that regenerates certs and deploys to all machines atomically. Partial deployment causes outages.

2. **Binary deployment workflow** -- Stop service -> SCP binary -> Start service. Consider a blue-green deployment with two binary paths.

3. **Passwordless sudo for service management** -- Add sudoers rule: `atlax-relay-op ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart atlax-relay, /usr/bin/systemctl stop atlax-relay, /usr/bin/systemctl start atlax-relay`

4. **Health check endpoint** -- The relay needs `/healthz` on the admin port for load balancer integration and monitoring.

5. **Log rotation** -- With JSON logging to journald, logs grow unbounded. Configure journald `MaxRetentionSec` or use logrotate.

6. **Monitoring** -- Add Prometheus metrics (Phase 4) and a Grafana dashboard for connection count, stream count, bytes transferred, and error rates.
