# UDP Tunneling

## Overview

In addition to TCP stream multiplexing, atlax supports tunneling UDP datagrams
over the TLS connection. This enables use cases that require UDP transport, such
as WireGuard tunnels, DNS resolution, and real-time protocols.

UDP tunneling uses three dedicated commands: UDP_BIND, UDP_DATA, and
UDP_UNBIND. Unlike TCP streams, UDP associations are connectionless and do not
follow the stream lifecycle state machine. Each UDP datagram is carried in a
single frame.

## Commands

### UDP_BIND (0x09)

Requests the relay to open a UDP listener on a specific address and port.

| Field | Value |
|-------|-------|
| Command | `0x09` (UDP_BIND) |
| Flags | `0x00` |
| Stream ID | 0 (connection-level) |
| Payload | Bind address as UTF-8 string (e.g., `0.0.0.0:51820`) |

The relay receives this request, opens a UDP socket on the specified address,
and begins forwarding incoming datagrams to the agent as UDP_DATA frames. The
relay responds with a UDP_BIND+ACK (Flags = `0x02`) on success, or STREAM_RESET
if the bind fails (port unavailable, permission denied, etc.).

### UDP_DATA (0x0A)

Carries a single UDP datagram between relay and agent.

| Field | Value |
|-------|-------|
| Command | `0x0A` (UDP_DATA) |
| Flags | `0x00` |
| Stream ID | Non-zero (identifies the UDP association) |
| Payload | See UDP_DATA Payload Format below |

UDP_DATA frames flow in both directions:

- **Relay to agent:** When the relay's UDP listener receives a datagram from an
  external client, it wraps the datagram in a UDP_DATA frame and sends it to
  the agent.
- **Agent to relay:** When the agent has a response datagram to send, it wraps
  it in a UDP_DATA frame and sends it to the relay, which transmits it from the
  UDP listener back to the original client.

### UDP_UNBIND (0x0B)

Requests the relay to close a previously opened UDP listener.

| Field | Value |
|-------|-------|
| Command | `0x0B` (UDP_UNBIND) |
| Flags | `0x00` |
| Stream ID | 0 (connection-level) |
| Payload | Bind address as UTF-8 string (must match the address used in UDP_BIND) |

After processing UDP_UNBIND, the relay closes the UDP socket and stops
forwarding datagrams. Any in-flight UDP_DATA frames for this association may be
silently dropped.

## UDP_DATA Payload Format

The UDP_DATA payload encodes the source address alongside the datagram content,
enabling the receiver to identify the originating client and route responses
correctly.

```
+------------------+------------------+------------------+
|   Addr Length    |   Source Addr    |    UDP Data      |
|    (1 byte)      |   (variable)     |   (variable)     |
+------------------+------------------+------------------+
```

| Field | Size | Description |
|-------|------|-------------|
| Addr Length | 1 byte | Length of the Source Addr field in bytes. |
| Source Addr | Variable (up to 255 bytes) | The source address of the UDP datagram as a UTF-8 string in `host:port` format (e.g., `192.168.1.50:12345`). |
| UDP Data | Remaining bytes | The raw UDP datagram payload. Size = Payload Length - 1 - Addr Length. |

Example payload for a 64-byte DNS query from `10.0.0.5:53214`:

```
Offset  Hex                                       Decoded
------  ----------------------------------------  -------------------------
0x00    0E                                        Addr Length: 14
0x01    31 30 2E 30 2E 30 2E 35 3A 35 33 32 31 34 Source Addr: "10.0.0.5:53214"
0x0F    [64 bytes of DNS query]                   UDP Data
```

Total payload size: 1 + 14 + 64 = 79 bytes.

## Limitations

UDP-over-TCP tunneling inherits fundamental limitations from the underlying TCP
transport:

### Head-of-Line Blocking

TCP guarantees in-order delivery. If a TCP segment is lost, all subsequent
segments (including those belonging to different UDP datagrams) are blocked
until the lost segment is retransmitted. This defeats UDP's tolerance for
out-of-order and lost packets, adding latency that would not exist on a native
UDP path.

### Added Latency

Each UDP datagram incurs the overhead of TCP acknowledgment and potential
retransmission. For latency-sensitive protocols (voice, video, gaming), this
overhead may be unacceptable.

### No Native Congestion Semantics

UDP applications that implement their own congestion control (such as QUIC or
WebRTC) may conflict with the TCP tunnel's congestion control, potentially
causing double congestion response.

### MTU Considerations

The effective MTU for tunneled UDP datagrams is reduced by the frame header
(12 bytes), the address encoding overhead (1 + address length), and the TLS
record overhead. Applications that assume a 1500-byte MTU may need to reduce
their datagram size or enable fragmentation.

## Use Cases

Despite the limitations, UDP tunneling is valuable for specific protocols:

### WireGuard

WireGuard uses UDP for its tunnel transport. Tunneling WireGuard UDP through
atlax enables a WireGuard peer behind CGNAT to be reachable via the relay's
public IP. The added latency from TCP encapsulation is acceptable for most
WireGuard use cases (site-to-site VPN, remote access) where the primary concern
is reachability, not sub-millisecond latency.

### DNS

DNS queries and responses are small UDP datagrams (typically under 512 bytes,
or under 4096 bytes with EDNS0). The latency overhead of TCP encapsulation is
minor for DNS resolution. This enables the relay to provide DNS forwarding for
customer services that need name resolution through the tunnel.

### Service Discovery

mDNS, SSDP, and similar service discovery protocols use UDP multicast or
broadcast. While atlax does not tunnel multicast natively, point-to-point
UDP forwarding can support directed service discovery queries.

## Future: QUIC Consideration

A future protocol version may introduce QUIC as an alternative tunnel transport
alongside or replacing TCP:

- **QUIC provides native multiplexing** with independent streams, eliminating
  head-of-line blocking for UDP datagrams carried on different streams.
- **QUIC runs over UDP**, allowing true UDP-over-UDP tunneling without TCP's
  in-order delivery constraint.
- **QUIC includes built-in encryption** (TLS 1.3), which aligns with atlax's
  security requirements.

The transition path:

1. QUIC tunnel transport would be introduced as an optional, parallel transport
   alongside the existing TCP+TLS tunnel.
2. The wire protocol (frame format, commands, flow control) remains the same
   regardless of the underlying transport.
3. Agents and relays negotiate the transport during connection establishment.
4. UDP_DATA frames carried over a QUIC tunnel would benefit from stream-level
   independence, recovering the native UDP semantics lost in the TCP tunnel.

This is explicitly a future consideration and is not part of the current
protocol specification.
