# System Architecture Overview

## Problem Statement

Customer nodes deployed in SMB and home office environments commonly sit behind
Carrier-Grade NAT (CGNAT) or ISPs that refuse to allocate static public IPv4
addresses. This makes inbound connections impossible. Services such as Samba,
HTTP portals, and monitoring agents become unreachable from external clients and
from the MSP control plane.

atlax solves this by reversing the connection direction. The customer node
(agent) dials *out* to a relay server on the public internet, establishing a
persistent mTLS tunnel. The relay then multiplexes client traffic back through
that tunnel to the agent, which forwards it to local services. No inbound ports
are needed on the customer network.

## Component Overview

atlax consists of three actors:

| Component | Binary | Location | Role |
|-----------|--------|----------|------|
| Relay | `atlax-relay` | Public VPS with static IP | Transport and routing layer. Accepts agent tunnels, accepts client connections, routes traffic. Never interprets tenant data. |
| Agent | `atlax-agent` | Customer node (behind CGNAT) | Tunnel initiator and local service forwarder. Dials out to relay, authenticates with mTLS, receives stream requests, forwards to local services. |
| Client | (any TCP client) | Internet / LAN | End user or application connecting to a service port on the relay. Unaware of the tunnel. |

## High-Level Data Flow

```
                          Public Internet
                               |
    +----------+         +-----+------+         +-------------+
    |  Client  | ------> |   Relay    | <------ |    Agent    |
    | (TCP/UDP)|  inbound| (public IP)|outbound | (behind NAT)|
    +----------+  conn   +------------+ TLS tun +------+------+
                                                       |
                                               +-------+-------+
                                               | Local Services |
                                               | (Samba, HTTP)  |
                                               +---------------+
```

**Direction of initiation:** The agent always dials out to the relay (outbound).
Clients always connect inbound to the relay. The relay bridges the two through
multiplexed streams over the agent's TLS tunnel.

## Detailed Component Diagram

```
+----------------------------------------------------------------------+
|                           RELAY (atlax-relay)                        |
|                                                                      |
|  +------------------+    +-------------------+    +-----------------+|
|  |   TLS Listener   |    |  Agent Registry   |    | Client Listener ||
|  |  (agent conns)   |--->|  (identity map)   |<---|  (TCP/UDP ports)||
|  +------------------+    +-------------------+    +-----------------+|
|          |                        |                        |         |
|          |                +-------+--------+               |         |
|          +--------------->|   Mux Router   |<--------------+         |
|                           | (stream-level  |                         |
|                           |  forwarding)   |                         |
|                           +----------------+                         |
+----------------------------------------------------------------------+
                                |
                     TLS 1.3 Tunnel (outbound from agent)
                                |
+----------------------------------------------------------------------+
|                        AGENT (atlax-agent)                           |
|                                                                      |
|  +------------------+    +-------------------+    +-----------------+|
|  |  Tunnel Client   |    |   Stream Demux    |    | Service Forward ||
|  |  (dials relay,   |--->| (route by stream  |--->| (dials local    ||
|  |   mTLS handshake)|    |   ID, per-conn)   |    |  service ports) ||
|  +------------------+    +-------------------+    +-----------------+|
|                                                                      |
+----------------------------------------------------------------------+
```

## Data Flow: Step by Step

The following six steps describe a complete request lifecycle:

1. **Agent connects.** The customer node runs `atlax-agent`, which dials the
   relay over TLS 1.3 with mutual authentication. The agent presents a client
   certificate signed by the Customer Intermediate CA. The relay verifies the
   chain up to the trusted Root CA.

2. **Registration.** After a successful mTLS handshake, the relay extracts the
   customer identity from the certificate's Common Name (`CN=customer-{uuid}`)
   and registers the agent in the Agent Registry. The registry maps the customer
   identity to the live tunnel connection.

3. **Client connects.** An external client opens a TCP connection to a service
   port on the relay (for example, port 10001 assigned to a particular
   customer). The Client Listener accepts the connection and looks up the Agent
   Registry to find the tunnel associated with that port's customer.

4. **Stream creation.** The Mux Router allocates a new stream ID (odd, since
   the relay initiates it) and sends a `STREAM_OPEN` frame to the agent through
   the existing TLS tunnel. The frame payload contains the target service
   address (for example, `127.0.0.1:445` for Samba).

5. **Forwarding.** The agent's Stream Demux receives the `STREAM_OPEN`, dials
   the local service, and begins bidirectional forwarding. `STREAM_DATA` frames
   carry bytes in both directions. Flow control (`WINDOW_UPDATE`) prevents
   either side from overwhelming the other.

6. **Teardown.** When either the client or the local service closes its side of
   the connection, a `STREAM_CLOSE` frame with the FIN flag propagates through
   the tunnel. The other side closes as well. On error, `STREAM_RESET` is used
   for immediate teardown.

## Component Responsibilities and Boundaries

### Relay (atlax-relay)

The relay is a **transport and routing layer only**. It never inspects,
decrypts, or interprets tenant application data (for example, SMB payloads). Its
responsibilities are:

- Accept and authenticate agent TLS connections (mTLS).
- Maintain the Agent Registry mapping customer identities to live connections.
- Accept client TCP/UDP connections on dedicated service ports.
- Route client traffic to the correct agent tunnel via the Mux Router.
- Enforce per-customer connection and stream limits.
- Emit operational metrics and health checks.
- Initiate graceful shutdown with GOAWAY when draining.

### Agent (atlax-agent)

The agent is the **tunnel initiator and local service proxy**. Its
responsibilities are:

- Establish and maintain a persistent outbound TLS tunnel to the relay.
- Authenticate with mTLS using a certificate issued by the Customer
  Intermediate CA.
- Demultiplex incoming streams and forward them to local services.
- Manage reconnection with exponential backoff and jitter on tunnel loss.
- Respond to PING frames with PONG for keepalive.
- Rotate certificates before expiry without restarting.
- Self-update when a new signed version is available from the control plane.

### Client (external)

The client is any TCP or UDP application connecting to a service port on the
relay. It has no knowledge of the tunnel infrastructure. From the client's
perspective, it is connecting directly to a service on the relay's IP address.

## Community vs Enterprise Architectural Differences

atlax is designed with extension points that separate Community Edition
functionality from Enterprise capabilities:

| Concern | Community Edition | Enterprise Edition |
|---------|-------------------|--------------------|
| Agent Registry | In-memory map (single relay) | Redis or etcd backed (shared across relays) |
| Relay topology | Single relay instance | Active-active with load balancer and shared state |
| Audit emission | Structured log output via `log/slog` | Event bus, SIEM integration, persistent audit store |
| Cross-relay routing | Not supported | Relay-to-relay forwarding when agent is on a different relay than the client |
| Certificate management | Manual or script-based | Integrated with AtlasShare control plane API for automated CSR signing |

The boundary is enforced through Go interfaces (`AgentRegistry`,
`audit.Emitter`). Community implementations satisfy these interfaces with local,
single-process backends. Enterprise implementations inject distributed backends.
No build tags or conditional compilation is used; the distinction is purely at
the dependency injection level.
