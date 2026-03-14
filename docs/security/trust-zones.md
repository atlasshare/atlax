# Trust Zone Model

## Overview

atlax operates across three distinct trust zones, each with different security
properties, trust levels, and data handling rules. Understanding these zones
is critical for making architectural decisions about where to place security
controls, what data can flow where, and what happens when a zone is
compromised.

## Zone Definitions

### Zone 1: Client Zone (Untrusted)

The Client zone encompasses all external entities that connect to the relay's
public-facing service ports. This includes end-user devices, third-party
applications, automated scripts, and any network traffic arriving from the
internet.

**Trust level:** None. All traffic from the Client zone is untrusted by default.

**Properties:**

- Clients connect over raw TCP or UDP to the relay's dedicated service ports.
- No authentication or encryption is required at this layer (the client
  connects to a plain TCP port; the tunnel between relay and agent provides the
  encrypted transport for the internal leg).
- The relay applies coarse protections: rate limiting per source IP, connection
  limits, and basic abuse detection.
- Clients have no visibility into the tunnel infrastructure. From their
  perspective, they are connecting to a service on the relay's IP.

**Boundary:** The relay's Client Listener is the boundary between the Client
zone and the Relay zone. Traffic crosses this boundary when the relay accepts a
client connection and creates a multiplexed stream on the agent's tunnel.

### Zone 2: Relay Zone (Transport and Routing Only)

The Relay zone encompasses the relay server itself: the TLS listener, Agent
Registry, Client Listener, and Mux Router. The relay is the bridge between the
untrusted Client zone and the trusted Agent zone.

**Trust level:** Limited. The relay is trusted to route traffic correctly and to
enforce tenant isolation, but it is explicitly not trusted with tenant
application data.

**Properties:**

- The relay handles mTLS authentication of agents and extracts tenant identity
  from certificates.
- The relay routes traffic between clients and agents based on port-to-customer
  mappings.
- The relay enforces per-customer limits (connections, streams, bandwidth).
- The relay does not interpret, store, or log application-layer data. It
  forwards opaque byte streams.
- The relay does not hold any customer business data, user credentials, or
  application state.
- Operational logs (connection events, stream counts, errors) are generated but
  contain no tenant data content.

**Boundary (Client side):** The Client Listener ports form the inbound boundary.
Client traffic enters the Relay zone here.

**Boundary (Agent side):** The TLS Listener for agent connections forms the
outbound boundary. Traffic exits the Relay zone into the Agent zone through
authenticated, encrypted TLS tunnels.

### Zone 3: Agent Zone (Customer Services)

The Agent zone encompasses the customer node where the agent runs, along with
the local services it forwards traffic to (Samba, HTTP servers, databases,
etc.). This zone contains the actual customer data and business logic.

**Trust level:** Trusted. The Agent zone is the customer's own environment.
Security within this zone is the customer's responsibility, with guidance from
atlax documentation.

**Properties:**

- The agent authenticates to the relay with mTLS, proving its identity
  cryptographically.
- The agent validates incoming stream requests against a configured service map
  (whitelist of allowed forward targets).
- The agent forwards traffic only to explicitly configured local service
  addresses (default: localhost only).
- The agent holds the customer's private key and certificate on disk with
  restrictive permissions.
- Customer data is accessed and processed by local services, not by the agent
  or relay.
- Audit events for stream lifecycle are logged locally on the agent.

**Boundary:** The agent's TLS client connection to the relay is the boundary
between the Agent zone and the Relay zone. Only authenticated, encrypted
traffic crosses this boundary.

## Zone Boundary Diagram

```
+---------------------+     +------------------------+     +---------------------+
|   CLIENT ZONE       |     |     RELAY ZONE         |     |    AGENT ZONE       |
|   (Untrusted)       |     |  (Transport/Routing)   |     |  (Customer Services)|
|                     |     |                        |     |                     |
|  End users          | TCP |  TLS Listener          | TLS |  Tunnel Agent       |
|  Applications       |---->|  Agent Registry        |<----|  Stream Demux       |
|  Scripts            |     |  Client Listener       |     |  Service Forwarder  |
|  Attackers          |     |  Mux Router            |     |  Local Services     |
|                     |     |                        |     |  (Samba, HTTP, ...)  |
+---------------------+     +------------------------+     +---------------------+
        |                           |                             |
   No trust, no               Limited trust,                Full trust,
   authentication              no tenant data               customer data
   at tunnel layer             access                       lives here
```

## Data Classification Per Zone

| Data Type | Client Zone | Relay Zone | Agent Zone |
|-----------|-------------|------------|------------|
| Application data (file contents, SMB payloads) | Sent by client, opaque to zone | Forwarded as opaque bytes, not inspected or stored | Processed by local services |
| Customer identity (certificate CN) | Not available | Extracted from mTLS handshake, used for routing | Embedded in agent certificate |
| Customer private key | Not available | Not available | Stored on agent disk (0600 permissions) |
| Service map (port-to-service mappings) | Not available | Port-to-customer mapping in config | Service-to-address mapping in config |
| Connection metadata (source IP, timestamps) | Source of the metadata | Logged for operational purposes | Logged for audit purposes |
| Relay private key | Not available | Stored on relay disk (0600 permissions) | Not available |
| TLS session state | Client-side TLS state (if applicable) | Session tickets for agent reconnection | Session cache for relay reconnection |

## Mapping to AtlasShare Trust Zones

atlax's three-zone model maps directly to the trust zones defined in the
AtlasShare platform architecture:

| atlax Zone | AtlasShare Equivalent | Description |
|------------|----------------------|-------------|
| Client Zone (Untrusted) | Client zone (untrusted devices) | External entities connecting over the internet. No inherent trust. Authentication and authorization happen deeper in the stack. |
| Relay Zone (Transport/Routing) | Edge/Relay zone (transport/routing only, no tenant data) | Public-facing infrastructure that routes traffic but does not access or store tenant data. Applies coarse protections (rate limiting, connection limits). |
| Agent Zone (Customer Services) | Application zone + Data zone (collapsed) | The customer's environment where business logic executes and data is stored. In AtlasShare, this maps to both the Application zone (API servers, business logic) and the Data zone (databases, file storage), collapsed into a single trust boundary because the agent forwards to both. |

### Why the Agent Zone Collapses Two AtlasShare Zones

In the full AtlasShare architecture, the Application zone (API servers, auth
middleware) and the Data zone (databases, object storage) are separate trust
boundaries with their own access controls. In atlax, the agent is a transparent
forwarder: it does not implement application logic or data access controls. The
agent forwards streams to local services, which may include both application
servers and data services on the same network. The security boundary between
application and data layers is enforced by those local services, not by atlax.

For customers who require strict separation between application and data
services, the agent's service map can be configured to forward specific ports to
specific hosts, but the trust boundary enforcement remains the responsibility of
the target services and the customer's network segmentation.

## Security Controls at Zone Boundaries

### Client Zone -> Relay Zone Boundary

| Control | Purpose |
|---------|---------|
| Rate limiting (per source IP) | Prevent connection flooding from a single source |
| Connection limits (per port) | Cap total client connections per service port |
| TCP SYN cookies | Mitigate SYN flood attacks at the OS level |
| Maximum frame payload validation | Reject oversized or malformed frames |
| Port-based tenant isolation | Ensure a client can only reach the customer assigned to that port |

### Relay Zone -> Agent Zone Boundary

| Control | Purpose |
|---------|---------|
| mTLS (TLS 1.3) | Mutual authentication, encrypted transport |
| Certificate chain validation | Verify agent identity via Customer Intermediate CA |
| Customer ID extraction and registration | Map authenticated identity to routing state |
| Per-customer stream limits | Prevent resource exhaustion by a single tenant |
| GOAWAY for graceful shutdown | Coordinated connection draining without data loss |
| CRL checking | Revoke compromised agent certificates |

### Within Agent Zone

| Control | Purpose |
|---------|---------|
| Service map validation | Whitelist of allowed forward targets, reject unexpected STREAM_OPEN targets |
| Localhost-only default | Forward only to 127.0.0.1 unless explicitly configured otherwise |
| File permission enforcement | Certificate and key files at 0600 |
| Audit logging | Log stream lifecycle events for forensic analysis |
| Certificate rotation | Limit exposure window for compromised credentials |

## Zone Compromise Impact Analysis

| Compromised Zone | Impact | Blast Radius | Recovery |
|------------------|--------|--------------|----------|
| Client Zone | Expected (always untrusted). Attackers can probe relay ports and attempt DoS. | Limited to relay public surface. No access to tunnel or agent. | Rate limiting, IP blocking, upstream DDoS mitigation. |
| Relay Zone | Attacker can observe forwarded traffic in memory, manipulate routing, impersonate the relay to agents. | All customers routed through the compromised relay. No direct access to agent-side local services without also compromising the tunnel. | Rotate relay certificates, migrate agents to a new relay, audit logs for unauthorized access. |
| Agent Zone | Attacker can access local services, steal agent private key, pivot within customer LAN. | Limited to the compromised customer. No cross-tenant impact (other customers use different certificates and port ranges). | Revoke agent certificate (CRL), rotate credentials, investigate customer network for lateral movement. |
