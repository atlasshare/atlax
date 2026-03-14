# Agent Registry API

The Agent Registry is the relay's internal store of connected agents. It maps customer IDs to active connections and provides lookup, heartbeat tracking, and lifecycle management. The registry is defined as an interface to support both Community (in-memory) and Enterprise (distributed) implementations.

---

## Interface Specification

```go
// AgentRegistry manages the set of connected agents.
// Community Edition provides an in-memory implementation.
// Enterprise Edition provides distributed implementations (Redis, etcd).
type AgentRegistry interface {
    // Register records a new agent connection. If an agent with the same
    // customerID is already registered, the existing connection is replaced
    // (the old connection should be closed by the caller after replacement).
    // Returns an error if registration fails (e.g., capacity exceeded).
    Register(ctx context.Context, info AgentInfo) error

    // Unregister removes an agent from the registry. This is called when
    // an agent disconnects or its connection is terminated. Returns an error
    // if the agent was not found.
    Unregister(ctx context.Context, customerID string) error

    // Lookup retrieves the connection information for a customer. Returns
    // ErrAgentNotFound if no agent is registered for the given customer ID.
    Lookup(ctx context.Context, customerID string) (*AgentInfo, error)

    // Heartbeat updates the LastSeen timestamp for an agent. Returns
    // ErrAgentNotFound if the agent is not registered.
    Heartbeat(ctx context.Context, customerID string) error

    // List returns all currently registered agents. Used by the control
    // plane API for administrative visibility.
    List(ctx context.Context) ([]AgentInfo, error)

    // Count returns the number of currently registered agents.
    Count() int
}
```

---

## AgentInfo Fields

```go
// AgentInfo holds the metadata and connection reference for a registered agent.
type AgentInfo struct {
    // CustomerID is the unique customer identifier extracted from the agent's
    // mTLS certificate CN (e.g., "customer-a1b2c3d4").
    CustomerID string

    // RelayID identifies which relay instance this agent is connected to.
    // Relevant in multi-relay (active-active) deployments.
    RelayID string

    // RemoteAddr is the agent's remote network address (IP:port) as seen
    // by the relay.
    RemoteAddr string

    // ConnectedAt is the timestamp when the agent's TLS handshake completed
    // and registration succeeded.
    ConnectedAt time.Time

    // LastSeen is the timestamp of the most recent heartbeat or data frame
    // received from the agent. Used for expiry detection.
    LastSeen time.Time

    // CertSerial is the serial number of the agent's mTLS certificate.
    // Used for audit logging and certificate tracking.
    CertSerial string

    // CertExpiry is the expiry time of the agent's mTLS certificate.
    // Used for proactive renewal monitoring.
    CertExpiry time.Time

    // AssignedPorts lists the TCP ports allocated to this customer on the
    // relay for client-facing connections.
    AssignedPorts []int

    // ActiveStreams is the current number of active multiplexed streams
    // for this agent.
    ActiveStreams int
}
```

---

## Registration Flow

The following sequence occurs when an agent connects to the relay:

1. **TLS handshake:** The relay's TLS listener accepts the connection and performs mTLS verification. The agent's certificate must be signed by a trusted Customer Intermediate CA.

2. **Identity extraction:** The relay extracts the customer ID from the peer certificate's Common Name (`CN=customer-{uuid}`). The certificate serial number and expiry are also recorded.

3. **Registration:** The relay calls `Registry.Register()` with the extracted `AgentInfo`. If an agent with the same `customerID` is already registered:
   - The existing connection is replaced (new connection wins).
   - The old connection is sent a GOAWAY frame and closed.
   - This handles reconnections where the relay has not yet detected the old connection's failure.

4. **Port allocation:** The relay assigns a range of TCP ports to the customer (based on configuration) and begins listening on those ports for client connections.

5. **Confirmation:** The relay logs the registration event and increments `atlax_relay_agents_connected`.

```
Agent                          Relay
  |                              |
  |--- TLS ClientHello -------->|
  |<-- TLS ServerHello ---------|
  |--- Client Certificate ----->|  (mTLS)
  |<-- Handshake Complete ------|
  |                              |  Extract CN=customer-{uuid}
  |                              |  Registry.Register(AgentInfo)
  |                              |  Allocate customer ports
  |                              |  Start client listeners
  |<-- PING --------------------|
  |--- PONG ------------------->|
  |                              |
```

---

## Heartbeat

The heartbeat mechanism detects dead connections that the TCP stack has not yet timed out.

### Timing

| Parameter | Value |
|-----------|-------|
| Heartbeat interval | 30 seconds |
| Heartbeat timeout (expiry) | 90 seconds |

### Flow

1. The relay sends a PING frame to each connected agent every 30 seconds.
2. The agent responds with a PONG frame.
3. On receiving PONG, the relay calls `Registry.Heartbeat(customerID)` to update `LastSeen`.
4. A background goroutine checks all registry entries every 30 seconds. Any agent whose `LastSeen` is older than 90 seconds is considered dead:
   - The agent's connection is closed.
   - The agent is unregistered from the registry.
   - Allocated customer ports are released.
   - An audit event is emitted.

### Agent-Side Heartbeat

The agent also monitors for PING frames from the relay. If no PING is received within 90 seconds, the agent assumes the connection is dead and initiates a reconnection.

---

## Lookup

The `Lookup` method is called by the relay's stream router when a client connects to a customer service port:

1. Client connects to a customer-assigned TCP port on the relay.
2. The relay looks up which customer ID owns that port.
3. The relay calls `Registry.Lookup(customerID)` to get the agent's connection.
4. If the agent is found, the relay opens a new multiplexed stream (STREAM_OPEN) to the agent.
5. If the agent is not found (`ErrAgentNotFound`), the relay closes the client connection with a TCP RST.

---

## Community Implementation: In-Memory Registry

The Community Edition provides an in-memory implementation using `sync.Map` for concurrent access.

### Characteristics

| Property | Value |
|----------|-------|
| Storage | `sync.Map` in process memory |
| Persistence | None (state lost on restart) |
| Scalability | Single relay only |
| Lookup time | O(1) |
| Thread safety | Lock-free reads via `sync.Map` |

### Behavior

- On relay restart, all agents must reconnect and re-register.
- No cross-relay lookup (unsuitable for active-active deployments).
- Suitable for deployments with a single relay and up to 1,000 agents.

### Expiry Cleanup

A background goroutine runs every 30 seconds, iterating over all entries and removing any with `LastSeen` older than 90 seconds. This handles cases where the TCP connection was silently dropped (e.g., agent node lost power).

---

## Enterprise Implementation: Redis

The Enterprise Edition provides a Redis-backed implementation for multi-relay deployments.

### Characteristics

| Property | Value |
|----------|-------|
| Storage | Redis hash per customer |
| Persistence | Redis persistence (RDB/AOF) |
| Scalability | Multi-relay, active-active |
| Lookup time | O(1) per Redis GET |
| Expiry | Redis TTL (90 seconds), refreshed on heartbeat |

### Behavior

- Each agent is stored as a Redis hash with fields from `AgentInfo`.
- The hash has a TTL of 90 seconds. Each heartbeat resets the TTL.
- If the TTL expires, Redis deletes the key automatically (dead agent cleanup).
- Any relay instance can look up any agent by customer ID, enabling cross-relay forwarding.
- On relay restart, agents reconnect and re-register. The Redis state is updated, not lost.

### Cross-Relay Forwarding

When a client connects to Relay A but the agent is registered on Relay B:

1. Relay A calls `Registry.Lookup(customerID)`.
2. The Redis-backed registry returns `AgentInfo` with `RelayID = "relay-b"` and `RelayAddr = "10.0.0.2:8443"`.
3. Relay A opens an internal connection to Relay B and forwards the stream.
4. Relay B routes the stream to the agent.

This cross-relay forwarding is transparent to both the client and the agent.
