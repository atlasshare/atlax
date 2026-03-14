# Flow Control

## Overview

Flow control prevents a fast sender from overwhelming a slow receiver. The atlax
wire protocol implements flow control at two levels:

- **Per-stream:** Each stream has its own receive window that limits how much
  data the sender can transmit before receiving a WINDOW_UPDATE.
- **Connection-level:** A single window covers all streams on a connection,
  providing an aggregate cap on in-flight data.

Both levels must be satisfied before a sender can transmit. A sender can only
send data if both the per-stream window and the connection-level window have
sufficient remaining capacity.

## Per-Stream Receive Window

Each stream starts with a receive window of **262,144 bytes (256KB)** by
default. This value is configurable at connection establishment.

The receive window represents the amount of data the receiver is willing to
buffer for that stream. As the sender transmits STREAM_DATA frames, the window
decreases by the payload size. When the window reaches zero, the sender must
stop transmitting on that stream until a WINDOW_UPDATE is received.

```
Initial state:

  Sender                          Receiver
    |                                |
    |  --- STREAM_DATA (32KB) --->   |  window: 256KB -> 224KB
    |  --- STREAM_DATA (32KB) --->   |  window: 224KB -> 192KB
    |  --- STREAM_DATA (32KB) --->   |  window: 192KB -> 160KB
    |                                |
    |  <-- WINDOW_UPDATE (96KB) --   |  window: 160KB -> 256KB
    |                                |  (receiver consumed data, restored window)
    |  --- STREAM_DATA (32KB) --->   |  window: 256KB -> 224KB
    |                                |
```

## Connection-Level Window

The connection-level window has a default size of **1,048,576 bytes (1MB)**. It
applies across all streams on the connection. Every STREAM_DATA frame reduces
both the per-stream window and the connection-level window.

WINDOW_UPDATE frames with Stream ID 0 adjust the connection-level window.
WINDOW_UPDATE frames with a non-zero Stream ID adjust only the per-stream
window.

A sender must check both windows before transmitting:

```
can_send = min(stream_window_remaining, connection_window_remaining)
```

If either window is exhausted, the sender blocks (or buffers, depending on
implementation) until a WINDOW_UPDATE replenishes the depleted window.

## WINDOW_UPDATE Semantics

The WINDOW_UPDATE frame increments the available window by the amount specified
in its 4-byte big-endian payload.

| Field | Value |
|-------|-------|
| Command | `0x07` (WINDOW_UPDATE) |
| Stream ID | 0 for connection-level, non-zero for per-stream |
| Payload | 4 bytes, big-endian unsigned integer: the increment in bytes |

Rules:

- The increment must be greater than zero. A WINDOW_UPDATE with a zero
  increment is a protocol error; the receiver should send GOAWAY and close the
  connection.
- The window size after applying the increment must not exceed 2^31 - 1
  (2,147,483,647 bytes). Overflow is a protocol error.
- WINDOW_UPDATE frames do not themselves consume window space. Only STREAM_DATA
  frames consume the window.

## Sender Blocking When Window Exhausted

When a sender's available window (per-stream or connection-level) reaches zero:

1. The sender stops reading from the data source (for example, the local TCP
   connection being forwarded) for the affected stream.
2. The sender enters a wait state, monitoring for incoming WINDOW_UPDATE frames.
3. When a WINDOW_UPDATE is received that restores capacity, the sender resumes
   reading and transmitting.

This blocking behavior naturally propagates backpressure to the original data
source. For example, if the agent's local Samba service is sending data faster
than the tunnel can deliver, the agent stops reading from the Samba connection,
which causes the kernel's TCP receive buffer to fill, which causes the Samba
server's TCP send buffer to fill, which causes Samba to slow down.

## Window Size Configuration

Both window sizes are configurable:

| Parameter | Default | Minimum | Maximum | Scope |
|-----------|---------|---------|---------|-------|
| Per-stream receive window | 256KB | 16KB | 16MB | Per stream |
| Connection-level receive window | 1MB | 64KB | 64MB | Per connection |

Configuration is set at agent and relay startup. Future protocol versions may
support in-band negotiation of window sizes during connection establishment.

Choosing appropriate window sizes involves balancing throughput and memory:

- **Larger windows** increase throughput on high-latency links by allowing more
  data in flight (similar to TCP window scaling).
- **Smaller windows** reduce memory consumption per stream, which matters when
  the relay serves thousands of concurrent streams.

A reasonable starting point: set the per-stream window to the bandwidth-delay
product of the expected link. For a 100 Mbps link with 20ms RTT:

```
BDP = 100 Mbps * 20 ms = 100,000,000 bits/s * 0.020 s = 250,000 bytes ~ 256KB
```

## Backpressure Propagation

Backpressure flows end-to-end through the following chain:

```
Client TCP conn
       |
       v
Relay: client read goroutine
       |
       v
Relay: stream send window check (per-stream + connection)
       |  (blocks here if window exhausted)
       v
TLS tunnel (relay -> agent)
       |
       v
Agent: stream demux
       |
       v
Agent: local service write
       |  (blocks here if local TCP send buffer full)
       v
Local service (e.g., Samba)
```

In the reverse direction (agent -> relay -> client), the same chain applies with
agent as sender and relay as receiver.

Key property: **no data is dropped due to flow control**. The sender blocks
rather than discarding frames. This is in contrast to UDP-style flow control
where excess packets are dropped.

## Deadlock Prevention

A naive flow control implementation can deadlock when both sides simultaneously
exhaust their send windows and neither can send WINDOW_UPDATE frames because
they are blocked on sending data.

atlax prevents this through the following mechanisms:

1. **Control frames bypass the data window.** WINDOW_UPDATE, PING, PONG, and
   GOAWAY frames are never subject to flow control. They are always written
   immediately, even when the data send window is exhausted. This ensures that
   window updates can always be delivered.

2. **Separate write queues.** The implementation uses a priority queue for
   outgoing frames. Control frames (WINDOW_UPDATE, PING, PONG, GOAWAY) are
   placed in a high-priority queue that is drained before data frames.

3. **Receiver-side consumption.** The receiver processes incoming STREAM_DATA
   frames and delivers them to the application layer (or local service)
   promptly. Once the application consumes the data, the receiver sends a
   WINDOW_UPDATE to replenish the window. Stalling the application layer does
   not prevent WINDOW_UPDATE from being sent; the receiver sends WINDOW_UPDATE
   based on buffer consumption, not application consumption.

4. **Connection-level window updates.** The receiver periodically sends
   connection-level WINDOW_UPDATE frames as data is consumed across all
   streams, even if individual streams have not consumed enough to warrant a
   per-stream update.

5. **Timeout detection.** If a sender is blocked for longer than a configurable
   timeout (default: 30 seconds) waiting for a WINDOW_UPDATE, it assumes a
   protocol error and resets the stream with STREAM_RESET. This prevents
   indefinite hangs.
