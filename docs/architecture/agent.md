# Tunnel Agent Architecture

## Role

The agent (`atlax-agent`) runs on customer nodes that are typically behind
CGNAT, dynamic IP, or restrictive firewall environments. It initiates an
outbound TLS tunnel to the relay server, authenticates with mTLS, and then
forwards incoming streams to local services such as Samba, HTTP, or custom
TCP applications.

The agent never accepts inbound connections from the internet. All connectivity
is outbound, which avoids the need for public IP addresses, port forwarding, or
dynamic DNS on the customer network.

## Components

### TLS Client

Establishes the outbound TLS 1.3 connection to the relay:

- Presents the customer certificate (signed by the Customer Intermediate CA).
- Verifies the relay's server certificate against the Relay Intermediate CA.
- Enables session ticket caching for fast reconnection.
- Supports certificate pinning as an additional relay identity check.

Configuration reference (Go `crypto/tls`):

```go
tlsConfig := &tls.Config{
    MinVersion:   tls.VersionTLS13,
    Certificates: []tls.Certificate{customerCert},
    RootCAs:      relayCACertPool,
    ServerName:   "relay.atlasshare.io",
}
```

### Stream Demuxer

Reads frames from the TLS tunnel and dispatches them by stream ID:

- Maintains a map of active stream IDs to local forwarding goroutines.
- On `STREAM_OPEN`: extracts the target service address from the payload, dials
  the local service, and creates a new forwarding pair.
- On `STREAM_DATA`: delivers the payload to the corresponding stream's write
  buffer.
- On `STREAM_CLOSE` (with FIN): signals the forwarding goroutine to perform a
  graceful half-close.
- On `STREAM_RESET`: immediately tears down the stream and closes the local
  connection.
- On `WINDOW_UPDATE`: adjusts the send window for the indicated stream.

Connection-level frames (stream ID 0) are handled separately:

- `PING`: immediately respond with `PONG`.
- `GOAWAY`: stop accepting new streams, drain existing ones, then reconnect.

### Service Forwarder

For each active stream, the Service Forwarder:

1. Dials the local service address specified in the `STREAM_OPEN` payload.
2. Starts two goroutines for bidirectional copy:
   - Local service -> tunnel stream (wraps bytes in `STREAM_DATA` frames).
   - Tunnel stream -> local service (unwraps `STREAM_DATA` payloads).
3. Applies flow control: pauses reading from the local service when the send
   window is exhausted, resumes on `WINDOW_UPDATE`.
4. On completion (either graceful close or reset), cleans up the stream entry
   in the Demuxer's stream map.

## Connection Lifecycle

```
Agent starts
    |
    v
Load configuration (relay address, service mappings, certificate paths)
    |
    v
Load customer certificate and relay CA pool
    |
    v
Dial relay over TLS 1.3 (mTLS handshake)
    |
    v
Handshake succeeds --> enter main read loop
    |
    +---> STREAM_OPEN     --> dial local service, start forwarding
    +---> STREAM_DATA     --> deliver to stream's local connection
    +---> STREAM_CLOSE    --> graceful half-close / full close
    +---> STREAM_RESET    --> abort stream
    +---> PING            --> respond with PONG
    +---> GOAWAY          --> drain streams, prepare to reconnect
    +---> WINDOW_UPDATE   --> adjust stream send window
    |
    v
On tunnel close --> reconnect with backoff
```

## Reconnection with Exponential Backoff and Jitter

When the TLS tunnel is lost (network failure, relay restart, GOAWAY), the
agent reconnects automatically:

1. **Initial delay:** 1 second.
2. **Backoff factor:** 2x on each consecutive failure.
3. **Maximum delay:** 60 seconds.
4. **Jitter:** Random value between 0 and 50% of the current delay, added to
   prevent thundering herd when many agents reconnect simultaneously.
5. **Reset:** On successful reconnection, the backoff state resets to the
   initial delay.

```
Attempt  Delay (without jitter)  Delay range (with jitter)
1        1s                      1.0s - 1.5s
2        2s                      2.0s - 3.0s
3        4s                      4.0s - 6.0s
4        8s                      8.0s - 12.0s
5        16s                     16.0s - 24.0s
6        32s                     32.0s - 48.0s
7+       60s                     60.0s - 90.0s (capped)
```

All reconnection attempts are logged at INFO level with the attempt number and
delay.

## Heartbeat (PING/PONG) Handling

The relay sends periodic `PING` frames (every 30 seconds) to detect stale
connections. The agent must respond with `PONG` within a configurable deadline
(default: 15 seconds). If the relay does not receive a `PONG`, it considers the
agent connection dead and unregisters it.

On the agent side:

- `PING` frames are handled in the main read loop with highest priority.
- `PONG` responses are sent immediately, outside the normal stream write queue.
- If the agent detects that it has not received a `PING` for an extended period
  (for example, 3x the expected interval), it proactively closes the connection
  and triggers reconnection, since this may indicate a half-open TCP state.

## Service Mapping Configuration

The agent configuration specifies which local services are available for
forwarding:

```yaml
services:
  - name: smb
    listen_port: 10001      # port on relay (informational)
    forward_to: 127.0.0.1:445
  - name: http
    listen_port: 10002
    forward_to: 127.0.0.1:8080
  - name: rdp
    listen_port: 10003
    forward_to: 192.168.1.100:3389
```

When the agent receives a `STREAM_OPEN` frame, the target address in the
payload is validated against the configured service map. If the target is not in
the allowed list, the agent responds with `STREAM_RESET` and logs a security
warning. This prevents the relay (or an attacker who compromises the relay) from
using the tunnel to reach arbitrary services on the customer network.

## Self-Update Mechanism

The agent supports automated binary updates with rollback:

1. **Version check.** Every 6 hours (configurable), the agent requests a signed
   version manifest from the control plane over HTTPS.

2. **Manifest verification.** The manifest is a JSON document signed with
   ed25519. The agent embeds the public key at compile time.

3. **Download.** If a newer version is available for the agent's OS and
   architecture, the agent downloads the binary over HTTPS and verifies the
   SHA256 checksum.

4. **Atomic replacement.** The new binary replaces the current one atomically
   (write to temp file, then rename).

5. **Restart.** The agent restarts via systemd notification or self-exec.

6. **Rollback.** If the new binary crashes within 60 seconds of startup, the
   previous binary is restored automatically.

All update operations are logged at INFO level. Failed signature or checksum
verification is logged at ERROR level and the update is aborted.

## Certificate Rotation

Agent certificates have a 90-day validity period. The agent manages rotation
without downtime:

1. **Expiry check.** On startup and every 24 hours, the agent checks the
   remaining validity of its current certificate.

2. **Renewal trigger.** When fewer than 30 days remain, the agent generates a
   new CSR (with a fresh key pair by default) and submits it to the AtlasShare
   control plane API over HTTPS.

3. **New certificate receipt.** The control plane signs the CSR with the
   Customer Intermediate CA and returns the new certificate.

4. **Validation.** The agent validates the new certificate's chain before
   accepting it.

5. **Hot reload.** The agent swaps the in-memory TLS certificate without
   dropping the existing tunnel connection. The new certificate is used on the
   next TLS handshake (reconnection or new connection).

6. **Overlap period.** The old certificate remains valid until its original
   expiry date, providing a safety window if the new certificate has issues.

Certificate files on disk are written atomically (write to temp, rename) and
have restrictive file permissions (0600).
