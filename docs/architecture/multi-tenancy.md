# Multi-Tenancy and Customer Isolation

## Tenant Model

In atlax, the **certificate identity is the tenant boundary**. Each customer
receives a unique client certificate with `CN=customer-{uuid}`, signed by the
Customer Intermediate CA. This certificate is the sole credential used to
authenticate the agent to the relay. There are no shared secrets, API keys, or
password-based authentication mechanisms.

The tenant boundary is enforced at every layer:

- **Authentication:** mTLS handshake verifies the certificate chain. An invalid
  or revoked certificate results in a rejected connection.
- **Registration:** The relay extracts the customer UUID from the certificate's
  Common Name and registers it in the Agent Registry.
- **Routing:** Client traffic is routed only to the agent whose customer ID
  matches the port-to-customer mapping. No cross-customer routing is possible
  through normal operation.
- **Stream scoping:** All streams exist within a single agent connection. There
  is no mechanism to address a stream on a different agent's connection.

## Port Allocation Strategy

Each customer receives dedicated TCP and UDP port ranges on the relay. This
provides network-level tenant isolation: a client connecting to a given port can
only reach the customer assigned to that port.

### Allocation Example

| Customer | Customer ID | TCP Ports | UDP Ports | Services |
|----------|-------------|-----------|-----------|----------|
| Acme Corp | customer-a1b2c3 | 10001-10010 | 10001-10010 | SMB (10001), HTTP (10002) |
| Beta LLC | customer-d4e5f6 | 10011-10020 | 10011-10020 | SMB (10011), RDP (10015) |
| Gamma Inc | customer-g7h8i9 | 10021-10030 | 10021-10030 | HTTP (10021) |

### Allocation Rules

- Port ranges are non-overlapping and configured per customer in the relay's
  YAML configuration.
- The relay validates at startup that no port conflicts exist.
- Ports below 1024 are not used (no privileged port binding required).
- Port ranges can be resized by updating configuration and reloading.
- The relay tracks active port listeners and refuses to start a listener on an
  already-bound port.

## Stream Isolation

Streams are scoped to the connection they belong to. The multiplexing protocol
enforces the following invariants:

- **Stream IDs are connection-local.** Stream ID 1 on Customer A's connection
  is completely independent of stream ID 1 on Customer B's connection.
- **No cross-connection addressing.** The wire protocol contains no mechanism
  to reference a stream on a different connection. The Mux Router only forwards
  frames within the same agent tunnel.
- **Relay-initiated stream IDs are odd.** When the relay opens a stream (to
  forward client traffic to an agent), it uses odd-numbered IDs (1, 3, 5, ...).
- **Agent-initiated stream IDs are even.** If the agent opens a stream (for
  future bidirectional use cases), it uses even-numbered IDs (2, 4, 6, ...).
- **Stream ID 0 is connection-level.** It is reserved for control frames
  (PING, PONG, GOAWAY, connection-level WINDOW_UPDATE) and cannot carry
  application data.

## Per-Customer Limits

To prevent a single customer from exhausting relay resources and impacting other
tenants, the relay enforces configurable per-customer limits:

### Connection Limits

| Limit | Default | Description |
|-------|---------|-------------|
| Max agent connections | 1 | Maximum concurrent tunnel connections per customer ID. A new connection from the same customer replaces the old one after graceful drain. |
| Max client connections | 100 | Maximum concurrent client connections routed to a single customer's agent. |

### Stream Limits

| Limit | Default | Description |
|-------|---------|-------------|
| Max concurrent streams | 256 | Maximum streams open simultaneously on a single agent tunnel. New STREAM_OPEN requests are rejected with STREAM_RESET when exceeded. |
| Max stream ID | 2^31 - 1 | After exhausting the stream ID space, the connection must be recycled (GOAWAY + reconnect). |

### Bandwidth Limits

| Limit | Default | Description |
|-------|---------|-------------|
| Per-stream rate | No limit | Configurable per-stream byte rate limit. |
| Per-customer aggregate rate | No limit | Configurable total throughput cap across all streams for a customer. |

Limits are enforced at the relay. When a limit is exceeded, the relay responds
with appropriate protocol-level signals (STREAM_RESET for stream limits, GOAWAY
for connection-level issues) and logs the event.

## Per-Customer Metrics

The relay emits metrics scoped to each customer identity, enabling per-tenant
observability:

| Metric | Type | Description |
|--------|------|-------------|
| `atlax_customer_streams_active` | Gauge | Currently open streams for the customer |
| `atlax_customer_streams_total` | Counter | Total streams opened since agent connected |
| `atlax_customer_bytes_sent` | Counter | Bytes sent from relay to agent (downstream) |
| `atlax_customer_bytes_received` | Counter | Bytes received from agent to relay (upstream) |
| `atlax_customer_connection_uptime_seconds` | Gauge | Duration of current tunnel connection |
| `atlax_customer_stream_errors_total` | Counter | Stream resets and errors |

Metrics are labeled with the customer ID and exposed via Prometheus. Dashboards
can be built per customer for MSP operations.

## MSP Operation Model

atlax supports Managed Service Provider (MSP) deployments where a single relay
serves many customers:

### Single Relay, Many Customers

```
Customer A (agent) ---+
                      |
Customer B (agent) ---+---> Relay (MSP-hosted VPS)  <--- Clients
                      |
Customer C (agent) ---+
```

In this model:

- The MSP provisions and operates the relay server.
- Each customer runs an agent on their node with their own certificate.
- The MSP manages certificate issuance through the AtlasShare control plane.
- Port allocation and service mapping are centrally managed by the MSP.
- Per-customer metrics enable SLA monitoring and usage billing.
- Customer isolation is guaranteed by the certificate-based tenant model and
  dedicated port ranges.

### Operational Responsibilities

| Responsibility | MSP | Customer |
|----------------|-----|----------|
| Relay infrastructure | Provision, monitor, maintain | None |
| Agent deployment | Provide installer/instructions | Run agent on their node |
| Certificate issuance | Manage through control plane | Store securely on agent |
| Port allocation | Assign and configure | None |
| Service mapping | Define in relay config | Configure local service endpoints in agent config |
| Monitoring | Per-customer dashboards | Access to their own metrics (Enterprise) |

## Community vs Enterprise Multi-Tenancy

| Capability | Community | Enterprise |
|------------|-----------|------------|
| Tenant isolation | Certificate identity + dedicated ports | Same, plus external registry validation |
| Relay topology | Single relay instance | Active-active with shared registry (Redis/etcd) |
| Agent Registry | In-memory map | Distributed store with cross-relay agent lookup |
| Cross-relay routing | Not available | Client on Relay 1 can reach agent on Relay 2 via internal relay-to-relay forwarding |
| Dynamic port allocation | Static YAML config | Control plane API for on-demand port assignment |
| Per-customer dashboards | Prometheus + manual Grafana setup | Integrated multi-tenant dashboard with RBAC |

### Enterprise Active-Active Architecture

```
                    +------------------+
                    |  Load Balancer   |
                    |  (TCP passthrough)|
                    +--------+---------+
                             |
              +--------------+--------------+
              |              |              |
        +-----+-----+  +----+------+  +----+------+
        |  Relay 1  |  |  Relay 2  |  |  Relay N  |
        +-----+-----+  +----+------+  +----+------+
              |              |              |
              +--------------+--------------+
                             |
                    +--------+---------+
                    |  Shared Registry |
                    |  (Redis / etcd)  |
                    +------------------+
```

In the Enterprise model, agents connect through a load balancer and may land on
any relay. The shared registry tracks which agent is connected to which relay.
When a client connects to a relay that does not hold the target agent's
connection, the relay forwards the traffic internally to the relay that does.
This provides high availability and horizontal scaling without requiring agents
to be aware of the relay topology.
