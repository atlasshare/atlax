package protocol

import (
	"errors"
	"fmt"
)

// Sentinel errors for UDP framing.
var (
	ErrInvalidUDPPayload = errors.New("invalid UDP_DATA payload")
	ErrUDPAddrTooLong    = errors.New("UDP source address exceeds 255 bytes")
)

// UDPDatagram represents a parsed UDP_DATA frame payload.
type UDPDatagram struct {
	SourceAddr string
	Payload    []byte
}

// ParseUDPDataPayload parses the UDP_DATA payload format:
// addr_length(1B) + source_addr(variable) + udp_payload.
func ParseUDPDataPayload(payload []byte) (*UDPDatagram, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("udp: parse: %w: empty payload", ErrInvalidUDPPayload)
	}

	addrLen := int(payload[0])
	if len(payload) < 1+addrLen {
		return nil, fmt.Errorf("udp: parse: %w: addr length %d but only %d bytes remain",
			ErrInvalidUDPPayload, addrLen, len(payload)-1)
	}

	addr := string(payload[1 : 1+addrLen])
	data := payload[1+addrLen:]

	// Return nil instead of empty slice for consistency
	if len(data) == 0 {
		data = nil
	}

	return &UDPDatagram{SourceAddr: addr, Payload: data}, nil
}

// BuildUDPDataPayload constructs the binary UDP_DATA payload from a source
// address and UDP data. The address must not exceed 255 bytes.
func BuildUDPDataPayload(sourceAddr string, data []byte) ([]byte, error) {
	if len(sourceAddr) > 255 {
		return nil, fmt.Errorf("udp: build: %w: length %d",
			ErrUDPAddrTooLong, len(sourceAddr))
	}

	addrLen := len(sourceAddr) // guaranteed <= 255 by check above
	buf := make([]byte, 0, 1+addrLen+len(data))
	buf = append(buf, byte(addrLen)) //nolint:gosec // addrLen <= 255
	buf = append(buf, []byte(sourceAddr)...)
	buf = append(buf, data...)
	return buf, nil
}

// NewUDPBindFrame creates a UDP_BIND frame requesting the relay to open
// a UDP listener on bindAddr.
func NewUDPBindFrame(bindAddr string) *Frame {
	return &Frame{
		Version:  ProtocolVersion,
		Command:  CmdUDPBind,
		StreamID: 0,
		Payload:  []byte(bindAddr),
	}
}

// NewUDPUnbindFrame creates a UDP_UNBIND frame requesting the relay to
// close a UDP listener on bindAddr.
func NewUDPUnbindFrame(bindAddr string) *Frame {
	return &Frame{
		Version:  ProtocolVersion,
		Command:  CmdUDPUnbind,
		StreamID: 0,
		Payload:  []byte(bindAddr),
	}
}

// NewUDPDataFrame creates a UDP_DATA frame carrying a datagram from
// sourceAddr with the given data payload.
func NewUDPDataFrame(streamID uint32, sourceAddr string, data []byte) (*Frame, error) {
	payload, err := BuildUDPDataPayload(sourceAddr, data)
	if err != nil {
		return nil, err
	}
	return &Frame{
		Version:  ProtocolVersion,
		Command:  CmdUDPData,
		StreamID: streamID,
		Payload:  payload,
	}, nil
}
