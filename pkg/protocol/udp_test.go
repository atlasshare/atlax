package protocol

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUDPDataPayload_Valid(t *testing.T) {
	// Format: addr_length(1B) + addr(variable) + udp_payload
	addr := "192.168.1.1:5353"
	payload := []byte("dns query data")

	raw := buildRawUDPPayload(addr, payload)

	dg, err := ParseUDPDataPayload(raw)
	require.NoError(t, err)
	assert.Equal(t, addr, dg.SourceAddr)
	assert.Equal(t, payload, dg.Payload)
}

func TestParseUDPDataPayload_MaxAddrLength(t *testing.T) {
	// Max addr length is 255 bytes
	addr := make([]byte, 255)
	for i := range addr {
		addr[i] = 'a'
	}
	payload := []byte("data")

	raw := buildRawUDPPayload(string(addr), payload)

	dg, err := ParseUDPDataPayload(raw)
	require.NoError(t, err)
	assert.Equal(t, string(addr), dg.SourceAddr)
	assert.Equal(t, payload, dg.Payload)
}

func TestParseUDPDataPayload_EmptyUDPPayload(t *testing.T) {
	addr := "10.0.0.1:53"
	raw := buildRawUDPPayload(addr, nil)

	dg, err := ParseUDPDataPayload(raw)
	require.NoError(t, err)
	assert.Equal(t, addr, dg.SourceAddr)
	assert.Empty(t, dg.Payload)
}

func TestParseUDPDataPayload_Truncated(t *testing.T) {
	// Only 1 byte (addr length) with no actual addr
	raw := []byte{0x05}
	_, err := ParseUDPDataPayload(raw)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidUDPPayload)
}

func TestParseUDPDataPayload_Empty(t *testing.T) {
	_, err := ParseUDPDataPayload(nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidUDPPayload)
}

func TestParseUDPDataPayload_AddrLenExceedsRemaining(t *testing.T) {
	// Claim addr is 100 bytes but only provide 5
	raw := []byte{100, 'a', 'b', 'c', 'd', 'e'}
	_, err := ParseUDPDataPayload(raw)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidUDPPayload)
}

func TestBuildUDPDataPayload_RoundTrip(t *testing.T) {
	addr := "127.0.0.1:51820"
	data := []byte("wireguard handshake")

	raw, err := BuildUDPDataPayload(addr, data)
	require.NoError(t, err)

	dg, err := ParseUDPDataPayload(raw)
	require.NoError(t, err)
	assert.Equal(t, addr, dg.SourceAddr)
	assert.Equal(t, data, dg.Payload)
}

func TestBuildUDPDataPayload_AddrTooLong(t *testing.T) {
	longAddr := make([]byte, 256)
	for i := range longAddr {
		longAddr[i] = 'x'
	}
	_, err := BuildUDPDataPayload(string(longAddr), []byte("data"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUDPAddrTooLong)
}

func TestNewUDPBindFrame(t *testing.T) {
	f := NewUDPBindFrame("0.0.0.0:5353")
	assert.Equal(t, CmdUDPBind, f.Command)
	assert.Equal(t, uint32(0), f.StreamID)
	assert.Equal(t, ProtocolVersion, f.Version)
	assert.Equal(t, []byte("0.0.0.0:5353"), f.Payload)
}

func TestNewUDPUnbindFrame(t *testing.T) {
	f := NewUDPUnbindFrame("0.0.0.0:5353")
	assert.Equal(t, CmdUDPUnbind, f.Command)
	assert.Equal(t, uint32(0), f.StreamID)
	assert.Equal(t, []byte("0.0.0.0:5353"), f.Payload)
}

func TestNewUDPDataFrame(t *testing.T) {
	f, err := NewUDPDataFrame(4, "10.0.0.1:1234", []byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, CmdUDPData, f.Command)
	assert.Equal(t, uint32(4), f.StreamID)
	assert.Equal(t, ProtocolVersion, f.Version)

	// Verify payload can be parsed back
	dg, err := ParseUDPDataPayload(f.Payload)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1:1234", dg.SourceAddr)
	assert.Equal(t, []byte("hello"), dg.Payload)
}

func TestNewUDPDataFrame_AddrTooLong(t *testing.T) {
	longAddr := make([]byte, 256)
	for i := range longAddr {
		longAddr[i] = 'x'
	}
	_, err := NewUDPDataFrame(4, string(longAddr), []byte("data"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUDPAddrTooLong)
}

// buildRawUDPPayload constructs the raw UDP_DATA payload format.
func buildRawUDPPayload(addr string, payload []byte) []byte {
	raw := make([]byte, 0, 1+len(addr)+len(payload))
	raw = append(raw, byte(len(addr)))
	raw = append(raw, []byte(addr)...)
	raw = append(raw, payload...)
	return raw
}
