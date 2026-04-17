# Wire Protocol Frame Format

## Overview

The atlax wire protocol is a custom binary framing protocol designed for
multiplexing TCP and UDP streams over a single TLS connection. Every message
exchanged between the relay and the agent is wrapped in a frame with a fixed
12-byte header followed by a variable-length payload.

Design goals:

- Minimal overhead (12 bytes per frame).
- Simple to implement and debug (fixed header, no variable-length header
  fields).
- Full flow control at stream and connection level.
- Keepalive and graceful shutdown built in.
- No external dependencies.

## Frame Header (12 Bytes)

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|    Version    |    Command    |     Flags     |   Reserved    |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                          Stream ID                            |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                       Payload Length                           |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

## Field Descriptions

| Offset | Field | Size | Endianness | Description |
|--------|-------|------|------------|-------------|
| 0 | Version | 1 byte | N/A | Protocol version. Current version is `0x01`. Peers must reject frames with unknown versions. |
| 1 | Command | 1 byte | N/A | Frame type. Identifies the operation (see Command Table below). |
| 2 | Flags | 1 byte | N/A | Bitfield for per-frame flags (see Flags section below). |
| 3 | Reserved | 1 byte | N/A | Reserved for future use. Must be set to `0x00` by senders. Receivers must ignore this field. |
| 4-7 | Stream ID | 4 bytes | Big-endian | Identifies the stream this frame belongs to. `0x00000000` indicates a connection-level frame. |
| 8-11 | Payload Length | 4 bytes | Big-endian | Length of the payload in bytes. Maximum value: 16,777,216 (16MB, `0x01000000`). Frames with larger payloads must be rejected. |

After the 12-byte header, exactly `Payload Length` bytes of payload follow. If
`Payload Length` is 0, there is no payload (the frame is header-only).

## Command Table

| Value | Name | Direction | Stream ID | Payload | Description |
|-------|------|-----------|-----------|---------|-------------|
| `0x01` | STREAM_OPEN | Both | Non-zero | Target address (UTF-8 string) | Open a new stream. Payload contains the target service address (e.g., `127.0.0.1:445`). |
| `0x02` | STREAM_DATA | Both | Non-zero | Application data | Carry data bytes for an open stream. |
| `0x03` | STREAM_CLOSE | Both | Non-zero | None (0 bytes) | Gracefully close a stream. Typically sent with the FIN flag. |
| `0x04` | STREAM_RESET | Both | Non-zero | Error code (4 bytes, big-endian) | Abort a stream immediately. The payload contains a 4-byte error code. |
| `0x05` | PING | Both | 0 | Opaque data (8 bytes) | Keepalive probe. The receiver must respond with PONG echoing the same payload. |
| `0x06` | PONG | Both | 0 | Opaque data (8 bytes) | Keepalive response. Payload must match the corresponding PING. |
| `0x07` | WINDOW_UPDATE | Both | 0 or non-zero | Window increment (4 bytes, big-endian) | Increase the flow control window. Stream ID 0 updates the connection-level window. Non-zero stream ID updates the per-stream window. |
| `0x08` | GOAWAY | Relay->Agent | 0 | Last stream ID (4 bytes) + error code (4 bytes) | Graceful shutdown. No new streams will be created. Existing streams continue until completion. |
| `0x09` | UDP_BIND | Agent->Relay | 0 | Bind address (UTF-8 string) | Request the relay to open a UDP listener on the specified address/port. |
| `0x0A` | UDP_DATA | Both | Non-zero | Addr length (1B) + source addr (variable) + UDP payload | Carry a UDP datagram. See UDP Tunneling documentation for payload format. |
| `0x0B` | UDP_UNBIND | Agent->Relay | 0 | Bind address (UTF-8 string) | Request the relay to close a UDP listener. |
| `0x0C` | UPDATE_MANIFEST | Reserved (enterprise) | 0 | Enterprise-defined | Reserved byte for the enterprise self-update manifest frame. Community builds do not emit this frame and ignore it on receipt. |
| `0x0D` | UPDATE_BINARY | Reserved (enterprise) | 0 | Enterprise-defined | Reserved byte for the enterprise self-update binary payload frame. Community builds do not emit this frame and ignore it on receipt. |
| `0x0E` | SERVICE_LIST | Agent->Relay | 0 | Newline-separated service names (UTF-8) | Advertise the agent's local service inventory to the relay. Sent once immediately after the mux handshake. Relay caches it for `GET /agents` exposure. See the SERVICE_LIST section below for payload rules. |

Commands `0x0F` through `0xFF` are reserved for future protocol extensions.

### SERVICE_LIST payload format

The `SERVICE_LIST` (0x0E) payload is a UTF-8 byte string containing zero or
more service names separated by a single newline character (`\n`, byte
`0x0A`). Service names themselves must not contain newline characters; the
sender is responsible for ensuring this invariant. Empty tokens (arising
from leading, trailing, or repeated newlines) are filtered out by the
receiver.

Rules:

- Stream ID MUST be `0` (connection-level frame).
- Payload MUST NOT exceed the global frame maximum (16 MB). In practice,
  payloads are tens to low-hundreds of bytes.
- The receiver MUST cap the number of parsed service names at 1024. Names
  beyond that limit are discarded with a warning log entry.
- The sender SHOULD skip emitting the frame entirely when it has no
  services to advertise (empty payload is legal but wasteful).
- Older agents that do not understand `SERVICE_LIST` never emit the frame.
  The relay waits a bounded time (50 ms) for the frame and proceeds with
  registration regardless; this is the forward-compat contract.

### Reserved bytes (0x0C, 0x0D)

Command bytes `0x0C` (`UPDATE_MANIFEST`) and `0x0D` (`UPDATE_BINARY`) are
reserved for the enterprise self-update feature. They are defined in the
community protocol enum so that community and enterprise builds agree on
byte assignments and never collide. Community builds neither emit nor
route these frames.

## Flags Bitfield

| Bit | Name | Description |
|-----|------|-------------|
| 0 (LSB) | FIN | End of stream. The sender will not send any more data on this stream. Used with STREAM_CLOSE for graceful shutdown and with STREAM_DATA for the final data frame. |
| 1 | ACK | Acknowledgment. Used with STREAM_OPEN to confirm stream creation (the receiver sends STREAM_OPEN+ACK back). |
| 2-7 | Reserved | Must be set to 0 by senders. Receivers must ignore these bits. |

Flag combinations:

| Flags byte | Meaning |
|------------|---------|
| `0x00` | No flags set |
| `0x01` | FIN only |
| `0x02` | ACK only |
| `0x03` | FIN + ACK |

## Stream ID Allocation Rules

Stream IDs are 32-bit unsigned integers with the following allocation:

| Initiator | Stream ID Parity | Range |
|-----------|------------------|-------|
| Relay | Odd | 1, 3, 5, ..., 2,147,483,647 |
| Agent | Even | 2, 4, 6, ..., 2,147,483,646 |
| Connection-level | Zero | 0 (reserved) |

Rules:

- Stream IDs must be assigned in strictly increasing order by each side.
- A stream ID must not be reused within the same connection. Once a stream
  reaches the Closed or Reset state, its ID is retired permanently.
- If the stream ID space is exhausted (all odd or all even IDs used), the
  connection must be recycled: the initiator sends GOAWAY, waits for active
  streams to drain, closes the connection, and reconnects.
- Stream ID 0 must never carry STREAM_OPEN, STREAM_DATA, STREAM_CLOSE, or
  STREAM_RESET frames. It is reserved exclusively for connection-level commands
  (PING, PONG, GOAWAY, connection-level WINDOW_UPDATE).

## Maximum Payload Size

The maximum payload length is **16,777,216 bytes (16MB)**. This limit is
enforced by both sender and receiver:

- Senders must not create frames with payloads exceeding 16MB.
- Receivers must reject frames whose Payload Length field exceeds 16MB by
  sending GOAWAY and closing the connection.

In practice, data frames (STREAM_DATA) should use much smaller payloads
(typically 4KB to 64KB) to maintain low latency and responsive flow control.

## Version Negotiation

The current protocol version is `0x01`. Version negotiation follows these rules:

- The agent connects and begins sending frames with its highest supported
  version.
- The relay inspects the Version field of the first frame received.
- If the relay supports the version, it proceeds normally.
- If the relay does not support the version, it sends a GOAWAY frame with an
  error code indicating version mismatch and closes the connection.
- Future versions may introduce a dedicated version negotiation handshake if
  the version space becomes non-trivial.

There is no in-band downgrade mechanism. Both sides must agree on the same
version for the duration of a connection.

## Wire Examples

### Example 1: PING Frame

A PING frame on the connection-level stream (ID 0) with 8 bytes of opaque data
(`0xDEADBEEFCAFEBABE`):

```
Offset  Hex                                       Decoded
------  ----------------------------------------  -------------------------
0x00    01                                        Version: 1
0x01    05                                        Command: PING (0x05)
0x02    00                                        Flags: none
0x03    00                                        Reserved
0x04    00 00 00 00                               Stream ID: 0 (connection)
0x08    00 00 00 08                               Payload Length: 8
0x0C    DE AD BE EF CA FE BA BE                   Payload: opaque ping data

Complete frame (20 bytes):
01 05 00 00 00 00 00 00 00 00 00 08 DE AD BE EF CA FE BA BE
```

### Example 2: STREAM_OPEN Frame

Opening stream ID 1 (relay-initiated, odd) with target address
`127.0.0.1:445` (14 bytes UTF-8):

```
Offset  Hex                                       Decoded
------  ----------------------------------------  -------------------------
0x00    01                                        Version: 1
0x01    01                                        Command: STREAM_OPEN (0x01)
0x02    00                                        Flags: none
0x03    00                                        Reserved
0x04    00 00 00 01                               Stream ID: 1
0x08    00 00 00 0E                               Payload Length: 14
0x0C    31 32 37 2E 30 2E 30 2E 31 3A 34 34 35   Payload: "127.0.0.1:445"

Complete frame (26 bytes):
01 01 00 00 00 00 00 01 00 00 00 0E 31 32 37 2E 30 2E 30 2E 31 3A 34 34 35
```

Note: The payload is the ASCII/UTF-8 encoding of the target address string. No
null terminator is included; the Payload Length field determines the boundary.

### Example 3: STREAM_DATA Frame with FIN

Sending the final 5 bytes of data (`Hello`) on stream ID 3, with the FIN flag
set to indicate no more data will follow:

```
Offset  Hex                                       Decoded
------  ----------------------------------------  -------------------------
0x00    01                                        Version: 1
0x01    02                                        Command: STREAM_DATA (0x02)
0x02    01                                        Flags: FIN (bit 0 set)
0x03    00                                        Reserved
0x04    00 00 00 03                               Stream ID: 3
0x08    00 00 00 05                               Payload Length: 5
0x0C    48 65 6C 6C 6F                            Payload: "Hello"

Complete frame (17 bytes):
01 02 01 00 00 00 00 03 00 00 00 05 48 65 6C 6C 6F
```

### Example 4: WINDOW_UPDATE Frame

Incrementing the flow control window for stream ID 5 by 65,536 bytes (64KB):

```
Offset  Hex                                       Decoded
------  ----------------------------------------  -------------------------
0x00    01                                        Version: 1
0x01    07                                        Command: WINDOW_UPDATE (0x07)
0x02    00                                        Flags: none
0x03    00                                        Reserved
0x04    00 00 00 05                               Stream ID: 5
0x08    00 00 00 04                               Payload Length: 4
0x0C    00 01 00 00                               Payload: 65536 (window increment)

Complete frame (16 bytes):
01 07 00 00 00 00 00 05 00 00 00 04 00 01 00 00
```
