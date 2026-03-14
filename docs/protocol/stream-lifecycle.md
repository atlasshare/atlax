# Stream Lifecycle

## Overview

A stream is a bidirectional logical channel within a single TLS tunnel
connection. Streams are lightweight (no additional TLS handshake) and are used
to carry individual TCP forwarding sessions or UDP associations. This document
describes the states a stream passes through, the frames that trigger
transitions, and the rules governing stream behavior.

## State Diagram

```
                      STREAM_OPEN (initiator sends)
                               |
                               v
                         +-----------+
                         |   Idle    |
                         +-----------+
                               |
                     STREAM_OPEN+ACK (peer responds)
                               |
                               v
                         +-----------+
             +---------->|   Open    |<----------+
             |           +-----------+           |
             |            /         \            |
             |  STREAM_DATA         STREAM_DATA  |
             |  (bidirectional)     (bidirectional)
             |            \         /            |
             |           STREAM_CLOSE+FIN        |
             |          (one side sends)         |
             |               |                   |
             |               v                   |
             |        +-------------+            |
             |        | Half-Closed |            |
             |        +-------------+            |
             |               |                   |
             |     STREAM_CLOSE+FIN              |
             |     (other side sends)            |
             |               |                   |
             |               v                   |
             |          +--------+               |
             |          | Closed |               |
             |          +--------+               |
             |                                   |
             |         STREAM_RESET              |
             +--------- (any state) ------------+
                               |
                               v
                          +-------+
                          | Reset |
                          +-------+
```

## States

| State | Description |
|-------|-------------|
| Idle | The stream ID has been allocated but STREAM_OPEN has not yet been acknowledged. |
| Open | Both sides have agreed to the stream (STREAM_OPEN+ACK received). Data can flow in both directions. |
| Half-Closed (local) | This side has sent STREAM_CLOSE+FIN. It will not send any more data, but it can still receive data from the peer. |
| Half-Closed (remote) | The peer has sent STREAM_CLOSE+FIN. This side can still send data, but will receive no more from the peer. |
| Closed | Both sides have sent STREAM_CLOSE+FIN. The stream is fully closed. Resources can be released after a brief hold period. |
| Reset | STREAM_RESET was sent or received. The stream is immediately torn down regardless of its previous state. |

## STREAM_OPEN: Initiation

The initiator (relay or agent) sends a STREAM_OPEN frame to request a new
stream:

| Field | Value |
|-------|-------|
| Command | `0x01` (STREAM_OPEN) |
| Flags | `0x00` (no flags) |
| Stream ID | Next available ID (odd for relay, even for agent) |
| Payload | Target address as UTF-8 string (e.g., `127.0.0.1:445`) |

The peer receives the STREAM_OPEN and must decide whether to accept or reject
the stream:

- **Accept:** The peer responds with a STREAM_OPEN frame on the same Stream ID
  with the ACK flag set (Flags = `0x02`). The stream transitions to Open.
- **Reject:** The peer responds with STREAM_RESET on the same Stream ID. The
  stream transitions to Reset and the ID is retired.

The target address in the payload tells the agent which local service to dial.
The agent validates this address against its configured service map before
accepting.

## STREAM_DATA: Data Transfer

Once a stream is in the Open state, either side can send STREAM_DATA frames:

| Field | Value |
|-------|-------|
| Command | `0x02` (STREAM_DATA) |
| Flags | `0x00` (normal) or `0x01` (FIN, final data frame) |
| Stream ID | The stream's ID |
| Payload | Application data bytes |

Rules:

- STREAM_DATA frames must only be sent when the stream is in Open or
  Half-Closed (remote) state (the sender's side is still open).
- Each STREAM_DATA frame consumes both the per-stream and connection-level flow
  control windows by the payload size.
- If the sender sets the FIN flag on a STREAM_DATA frame, it is equivalent to
  sending the data followed by STREAM_CLOSE+FIN. The stream transitions to
  Half-Closed (local) on the sender's side.
- Empty STREAM_DATA frames (payload length 0, no FIN) are permitted but have
  no effect and should be avoided.

## STREAM_CLOSE: Graceful Close

Graceful stream closure uses STREAM_CLOSE with the FIN flag:

| Field | Value |
|-------|-------|
| Command | `0x03` (STREAM_CLOSE) |
| Flags | `0x01` (FIN) |
| Stream ID | The stream's ID |
| Payload | None (0 bytes) |

The graceful close sequence mirrors TCP's FIN handshake:

```
Side A                              Side B
  |                                    |
  |  --- STREAM_CLOSE+FIN --->         |  A enters Half-Closed (local)
  |                                    |  B enters Half-Closed (remote)
  |                                    |
  |  (B may continue sending data)     |
  |  <-- STREAM_DATA (optional) ---    |
  |                                    |
  |  <-- STREAM_CLOSE+FIN ---          |  B enters Half-Closed (local)
  |                                    |  Both sides now Closed
  |                                    |
```

After both sides have sent STREAM_CLOSE+FIN, the stream is fully closed. Both
sides release stream resources (buffer memory, goroutines, stream map entries).

## STREAM_RESET: Error Handling

STREAM_RESET immediately terminates a stream from any state:

| Field | Value |
|-------|-------|
| Command | `0x04` (STREAM_RESET) |
| Flags | `0x00` |
| Stream ID | The stream's ID |
| Payload | 4 bytes, big-endian error code |

Error codes:

| Code | Name | Description |
|------|------|-------------|
| `0x00000000` | NO_ERROR | Clean reset (e.g., client disconnected normally) |
| `0x00000001` | PROTOCOL_ERROR | Peer violated the protocol |
| `0x00000002` | INTERNAL_ERROR | Implementation error |
| `0x00000003` | REFUSED | Stream was refused (e.g., target not in service map) |
| `0x00000004` | CONNECT_FAILED | Could not connect to target service |
| `0x00000005` | TIMEOUT | Operation timed out |

When STREAM_RESET is sent or received:

1. All pending data for the stream is discarded.
2. The local connection (if any) is closed immediately.
3. The stream transitions to the Reset state.
4. The stream ID is retired and must not be reused.
5. Flow control window space consumed by the stream is reclaimed (the receiver
   sends a connection-level WINDOW_UPDATE to account for discarded data).

STREAM_RESET can be sent from any state: Idle, Open, Half-Closed, or even in
response to a STREAM_OPEN that the peer has not yet acknowledged.

## Half-Closed Semantics

The Half-Closed state allows one side to finish sending while still receiving
from the peer. This is important for protocols where the request is fully sent
before the response begins (for example, an HTTP request/response over the
tunnel).

### Half-Closed (Local)

This side has sent STREAM_CLOSE+FIN. It must not send any more STREAM_DATA
frames on this stream. It can still receive STREAM_DATA from the peer and must
continue processing incoming data until the peer also sends STREAM_CLOSE+FIN.

### Half-Closed (Remote)

The peer has sent STREAM_CLOSE+FIN. This side will not receive any more
STREAM_DATA from the peer. It can still send data. Receiving STREAM_DATA from
the peer in this state is a protocol error and should trigger STREAM_RESET.

## Stream ID Reuse Rules

Stream IDs are **never reused** within a single connection. Once a stream
reaches the Closed or Reset state, its ID is permanently retired. The rationale:

- Prevents confusion between old and new streams if frames are delayed.
- Simplifies the state machine (no need to handle a stream ID appearing in an
  unexpected state).
- Stream ID exhaustion is extremely unlikely in practice (over 1 billion IDs
  per side).

If the stream ID space is genuinely exhausted, the connection must be recycled:

1. The side that exhausted its IDs sends GOAWAY.
2. Both sides wait for active streams to drain.
3. The connection is closed.
4. A new connection is established with fresh stream ID counters.

## Concurrent Stream Limits

The number of simultaneously open streams is bounded by configurable limits:

| Limit | Default | Scope |
|-------|---------|-------|
| Max concurrent streams (relay-initiated) | 256 | Per agent connection |
| Max concurrent streams (agent-initiated) | 256 | Per agent connection |

When the limit is reached, the initiator must wait for an existing stream to
close before opening a new one. If the initiator sends a STREAM_OPEN that would
exceed the limit, the peer responds with STREAM_RESET (error code: REFUSED).

These limits protect against resource exhaustion and ensure fair sharing of
relay capacity across multiple customers in a multi-tenant deployment.
