# Relay Server Architecture

## Role

The relay (`atlax-relay`) is the **transport and routing layer** of the atlax
system. It runs on a public VPS with a static IP address and serves two
audiences simultaneously:

- **Agents** connect inbound over TLS 1.3 with mutual authentication.
- **Clients** connect inbound over raw TCP (or UDP) to dedicated service ports.

The relay bridges these two connection types by routing client traffic through
multiplexed streams on the agent's TLS tunnel. The relay never interprets,
decrypts, or stores tenant application data. It forwards opaque byte streams
between clients and agents.

## Components

### TLS Listener (Agent Connections)

Listens on a dedicated port (default: 8443) for agent TLS connections. Enforces:

- TLS 1.3 minimum version.
- `RequireAndVerifyClientCert` mode (mTLS).
- Client certificate validation against the Customer Intermediate CA pool.
- Session ticket support for fast reconnection.

After a successful handshake, the listener extracts the customer identity from
the peer certificate's Common Name (`CN=customer-{uuid}`) and passes the
authenticated connection to the Agent Registry.

### Agent Registry

Maps customer identities to live tunnel connections. Provides the lookup
interface used by the Mux Router when routing client traffic.

```
Interface: AgentRegistry
  Register(customerID, conn)    -- called after mTLS handshake
  Unregister(customerID)        -- called on tunnel close
  Lookup(customerID) -> conn    -- called when client traffic arrives
  Heartbeat(customerID)         -- called on PONG receipt
```

**Community implementation:** In-memory `sync.RWMutex`-protected map. Suitable
for a single relay instance serving up to thousands of agents.

**Enterprise implementation:** Backed by Redis or etcd. Stores agent location
metadata (relay ID, internal address) to enable cross-relay routing when
multiple relays share a load balancer.

### Client Listener (TCP/UDP Service Ports)

Listens on dedicated TCP and UDP ports assigned to each customer. When a client
connects, the listener:

1. Determines the customer identity from the port-to-customer mapping.
2. Looks up the agent connection in the Agent Registry.
3. Passes the client connection to the Mux Router for stream creation.

Port-to-customer mappings are loaded from configuration and can be reloaded at
runtime without restart.

### Mux Router

The central routing component. It bridges client connections and agent tunnel
streams:

1. Receives a client connection and the target agent's tunnel connection from
   the Client Listener.
2. Allocates a new stream ID (odd, since the relay initiates).
3. Sends a `STREAM_OPEN` frame to the agent with the target service address in
   the payload.
4. Begins bidirectional forwarding of `STREAM_DATA` frames between the client
   connection and the tunnel stream.
5. Handles `STREAM_CLOSE`, `STREAM_RESET`, and flow control (`WINDOW_UPDATE`).

Each stream runs in its own goroutine pair (one for each copy direction).

## Connection Lifecycle

```
Agent dials relay
       |
       v
TLS Listener accepts connection
       |
       v
mTLS handshake (TLS 1.3, verify client cert chain)
       |
       v
Extract identity: CN=customer-{uuid}
       |
       v
Agent Registry: Register(customerID, conn)
       |
       v
Enter read loop: dispatch frames by command type
       |
       +---> PONG received     --> Registry.Heartbeat(customerID)
       +---> STREAM_DATA       --> forward to associated client conn
       +---> STREAM_CLOSE      --> tear down stream, close client conn
       +---> STREAM_RESET      --> abort stream, close client conn
       +---> WINDOW_UPDATE     --> update flow control window
       +---> GOAWAY            --> (agent should not send this; log warning)
       |
       v
On tunnel close: Unregister(customerID), close all active streams
```

## Port Allocation Model

Each customer receives a dedicated range of TCP and UDP ports on the relay:

| Customer | TCP Ports | UDP Ports | Service Mapping |
|----------|-----------|-----------|-----------------|
| Customer A | 10001-10010 | 10001-10010 | 10001=SMB, 10002=HTTP |
| Customer B | 10011-10020 | 10011-10020 | 10011=SMB, 10015=RDP |

Port ranges are configurable per customer in the relay's YAML configuration.
The relay tracks port availability to prevent conflicts. Firewall rules are
generated or validated per customer allocation.

This model provides strong tenant isolation at the network level: a client
connecting to port 10001 can only reach Customer A's agent, regardless of any
other state.

## Graceful Shutdown

When the relay receives a shutdown signal (SIGTERM, SIGINT):

1. Stop accepting new agent connections on the TLS Listener.
2. Stop accepting new client connections on all Client Listeners.
3. Send a `GOAWAY` frame to every connected agent. The GOAWAY frame signals
   that no new streams will be created, but existing streams will complete.
4. Wait for all active streams to finish (with a configurable timeout).
5. Close all agent connections.
6. Exit.

This sequence ensures that in-flight requests complete without data loss, while
preventing new work from starting on a draining relay.

## Scaling Characteristics

### Goroutine Model

The relay uses a goroutine-per-stream model:

- One goroutine reads from the client connection and writes to the tunnel.
- One goroutine reads from the tunnel stream and writes to the client.
- Total goroutines per active stream: 2.
- At 1,000 agents with 100 concurrent streams each: 200,000 goroutines. Go's
  scheduler handles this efficiently with a per-goroutine stack starting at 8KB.

### Memory Management

- Frame read/write buffers are pooled using `sync.Pool` to reduce GC pressure.
- The default maximum frame payload is 16MB, but typical data frames are much
  smaller (4-64KB).
- Per-stream flow control windows (256KB default) bound memory consumption per
  stream.
- Per-connection flow control windows (1MB default) bound total memory per
  agent tunnel.

### Scaling Targets

| Metric | Target |
|--------|--------|
| Concurrent agent connections | 1,000+ |
| Concurrent streams per agent | 100+ |
| Per-stream throughput | 100 Mbps |
| Total relay memory | Less than 4GB for 1,000 agents |
| mTLS handshake latency | Less than 50ms (with session resumption) |

### Bottlenecks and Mitigations

| Bottleneck | Mitigation |
|------------|------------|
| TLS handshake CPU | Session resumption reduces repeat handshakes to 1-RTT or 0-RTT |
| Memory per connection | sync.Pool for buffers, flow control caps per-stream memory |
| File descriptors | Increase ulimit; each agent = 1 fd, each client = 1 fd |
| Single relay throughput | Enterprise: active-active with load balancer and shared registry |

## Enterprise Extension Points

The relay is designed with interface boundaries that Enterprise editions can
replace:

| Extension Point | Community | Enterprise |
|-----------------|-----------|------------|
| AgentRegistry | In-memory map | Redis/etcd with TTL and cross-relay lookup |
| Port Allocator | Static YAML config | Dynamic allocation via control plane API |
| Metrics Backend | Prometheus exposition | Additional push to Datadog, CloudWatch |
| Cross-Relay Routing | Not supported | Relay-to-relay gRPC or TCP forwarding |
| Audit Emitter | Structured log | Event bus (Kafka, NATS) for SIEM integration |

Enterprise implementations are injected at startup through dependency injection.
No build tags or conditional compilation is needed.
