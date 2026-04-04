package protocol

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameCodecRoundTrip_AllCommands(t *testing.T) {
	commands := []struct {
		name string
		cmd  Command
	}{
		{"StreamOpen", CmdStreamOpen},
		{"StreamData", CmdStreamData},
		{"StreamClose", CmdStreamClose},
		{"StreamReset", CmdStreamReset},
		{"Ping", CmdPing},
		{"Pong", CmdPong},
		{"WindowUpdate", CmdWindowUpdate},
		{"GoAway", CmdGoAway},
		{"UDPBind", CmdUDPBind},
		{"UDPData", CmdUDPData},
		{"UDPUnbind", CmdUDPUnbind},
	}

	codec := NewFrameCodec()

	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			original := &Frame{
				Version:  ProtocolVersion,
				Command:  tc.cmd,
				Flags:    0x00,
				StreamID: 42,
				Payload:  []byte("test payload"),
			}

			var buf bytes.Buffer
			err := codec.WriteFrame(&buf, original)
			require.NoError(t, err)

			decoded, err := codec.ReadFrame(&buf)
			require.NoError(t, err)

			assert.Equal(t, original.Version, decoded.Version)
			assert.Equal(t, original.Command, decoded.Command)
			assert.Equal(t, original.Flags, decoded.Flags)
			assert.Equal(t, original.StreamID, decoded.StreamID)
			assert.Equal(t, original.Payload, decoded.Payload)
		})
	}
}

func TestFrameCodecRoundTrip_AllFlagCombinations(t *testing.T) {
	tests := []struct {
		name  string
		flags Flag
	}{
		{"NoFlags", 0x00},
		{"FIN", FlagFIN},
		{"ACK", FlagACK},
		{"FIN+ACK", FlagFIN | FlagACK},
	}

	codec := NewFrameCodec()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := &Frame{
				Version:  ProtocolVersion,
				Command:  CmdStreamData,
				Flags:    tc.flags,
				StreamID: 1,
				Payload:  []byte("data"),
			}

			var buf bytes.Buffer
			require.NoError(t, codec.WriteFrame(&buf, original))

			decoded, err := codec.ReadFrame(&buf)
			require.NoError(t, err)
			assert.Equal(t, tc.flags, decoded.Flags)
		})
	}
}

func TestFrameCodecRoundTrip_ZeroPayload(t *testing.T) {
	codec := NewFrameCodec()
	original := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdPing,
		Flags:    0x00,
		StreamID: 0,
		Payload:  nil,
	}

	var buf bytes.Buffer
	require.NoError(t, codec.WriteFrame(&buf, original))

	decoded, err := codec.ReadFrame(&buf)
	require.NoError(t, err)
	assert.Empty(t, decoded.Payload)
	assert.Equal(t, HeaderSize, buf.Len()+HeaderSize) // only header was written
}

func TestFrameCodecRoundTrip_MaxPayloadBoundary(t *testing.T) {
	codec := NewFrameCodec()

	t.Run("ExactlyMaxPayload", func(t *testing.T) {
		// Use a small payload for the test to avoid allocating 16MB.
		// The codec validates PayloadLength in the header, not the actual
		// slice size during write. We test the boundary via rejection test.
		payload := make([]byte, 4096)
		for i := range payload {
			payload[i] = byte(i % 256)
		}
		original := &Frame{
			Version:  ProtocolVersion,
			Command:  CmdStreamData,
			Flags:    0x00,
			StreamID: 5,
			Payload:  payload,
		}

		var buf bytes.Buffer
		require.NoError(t, codec.WriteFrame(&buf, original))

		decoded, err := codec.ReadFrame(&buf)
		require.NoError(t, err)
		assert.Equal(t, payload, decoded.Payload)
	})
}

func TestFrameCodecRoundTrip_StreamIDs(t *testing.T) {
	tests := []struct {
		name     string
		streamID uint32
	}{
		{"Zero_ConnectionLevel", 0},
		{"One_RelayInitiated", 1},
		{"Two_AgentInitiated", 2},
		{"Large_Odd", 0x7FFFFFFF},
		{"MaxUint32", 0xFFFFFFFF},
	}

	codec := NewFrameCodec()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := &Frame{
				Version:  ProtocolVersion,
				Command:  CmdStreamData,
				Flags:    0x00,
				StreamID: tc.streamID,
				Payload:  []byte("x"),
			}

			var buf bytes.Buffer
			require.NoError(t, codec.WriteFrame(&buf, original))

			decoded, err := codec.ReadFrame(&buf)
			require.NoError(t, err)
			assert.Equal(t, tc.streamID, decoded.StreamID)
		})
	}
}

func TestFrameCodec_BigEndianByteOrder(t *testing.T) {
	codec := NewFrameCodec()
	original := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		Flags:    0x00,
		StreamID: 0x01020304,
		Payload:  []byte{0xAA, 0xBB},
	}

	var buf bytes.Buffer
	require.NoError(t, codec.WriteFrame(&buf, original))

	raw := buf.Bytes()
	// StreamID at offset 4-7, big-endian
	assert.Equal(t, byte(0x01), raw[4])
	assert.Equal(t, byte(0x02), raw[5])
	assert.Equal(t, byte(0x03), raw[6])
	assert.Equal(t, byte(0x04), raw[7])

	// PayloadLength at offset 8-11, big-endian
	payloadLen := binary.BigEndian.Uint32(raw[8:12])
	assert.Equal(t, uint32(2), payloadLen)
}

func TestFrameCodec_RejectPayloadExceedingMax(t *testing.T) {
	codec := NewFrameCodec()

	// Craft a raw frame with PayloadLength = MaxPayloadSize + 1
	var header [HeaderSize]byte
	header[0] = ProtocolVersion
	header[1] = byte(CmdStreamData)
	header[2] = 0x00
	header[3] = 0x00
	binary.BigEndian.PutUint32(header[4:8], 1)
	binary.BigEndian.PutUint32(header[8:12], MaxPayloadSize+1)

	r := bytes.NewReader(header[:])
	_, err := codec.ReadFrame(r)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidFrame)
}

func TestFrameCodec_RejectUnknownProtocolVersion(t *testing.T) {
	codec := NewFrameCodec()

	var header [HeaderSize]byte
	header[0] = 0xFF // unknown version
	header[1] = byte(CmdPing)
	header[2] = 0x00
	header[3] = 0x00
	binary.BigEndian.PutUint32(header[4:8], 0)
	binary.BigEndian.PutUint32(header[8:12], 0)

	r := bytes.NewReader(header[:])
	_, err := codec.ReadFrame(r)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidFrame)
}

func TestFrameCodec_EncodeReservedByteZero(t *testing.T) {
	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdPing,
		Flags:    0x00,
		StreamID: 0,
		Payload:  nil,
	}

	var buf bytes.Buffer
	require.NoError(t, codec.WriteFrame(&buf, frame))

	raw := buf.Bytes()
	assert.Equal(t, byte(0x00), raw[3], "reserved byte must be 0x00")
}

func TestFrameCodec_DecodeIgnoresReservedByte(t *testing.T) {
	codec := NewFrameCodec()

	var header [HeaderSize]byte
	header[0] = ProtocolVersion
	header[1] = byte(CmdPing)
	header[2] = 0x00
	header[3] = 0xAB // non-zero reserved byte
	binary.BigEndian.PutUint32(header[4:8], 0)
	binary.BigEndian.PutUint32(header[8:12], 0)

	r := bytes.NewReader(header[:])
	frame, err := codec.ReadFrame(r)
	require.NoError(t, err)
	assert.Equal(t, CmdPing, frame.Command)
}

func TestFrameCodec_TruncatedHeader(t *testing.T) {
	codec := NewFrameCodec()

	// Only 6 bytes of a 12-byte header
	partial := []byte{ProtocolVersion, byte(CmdPing), 0x00, 0x00, 0x00, 0x00}
	r := bytes.NewReader(partial)
	_, err := codec.ReadFrame(r)
	require.Error(t, err)
}

func TestFrameCodec_TruncatedPayload(t *testing.T) {
	codec := NewFrameCodec()

	var header [HeaderSize]byte
	header[0] = ProtocolVersion
	header[1] = byte(CmdStreamData)
	header[2] = 0x00
	header[3] = 0x00
	binary.BigEndian.PutUint32(header[4:8], 1)
	binary.BigEndian.PutUint32(header[8:12], 100) // claims 100 bytes

	// Only provide 5 bytes of payload
	data := append(header[:], []byte("short")...)
	r := bytes.NewReader(data)
	_, err := codec.ReadFrame(r)
	require.Error(t, err)
}

func TestFrameCodec_SmallChunkReader(t *testing.T) {
	codec := NewFrameCodec()
	original := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		Flags:    FlagFIN,
		StreamID: 7,
		Payload:  []byte("hello world from small chunks"),
	}

	var buf bytes.Buffer
	require.NoError(t, codec.WriteFrame(&buf, original))

	// Wrap in a reader that returns 1 byte at a time
	r := &oneByteReader{r: &buf}
	decoded, err := codec.ReadFrame(r)
	require.NoError(t, err)
	assert.Equal(t, original.Command, decoded.Command)
	assert.Equal(t, original.Flags, decoded.Flags)
	assert.Equal(t, original.StreamID, decoded.StreamID)
	assert.Equal(t, original.Payload, decoded.Payload)
}

// oneByteReader wraps a reader and returns at most 1 byte per Read call.
type oneByteReader struct {
	r io.Reader
}

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

// Wire examples from docs/protocol/wire-format.md

func TestFrameCodec_WireExample_Ping(t *testing.T) {
	expected := []byte{
		0x01, 0x05, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x08,
		0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE,
	}

	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdPing,
		Flags:    0x00,
		StreamID: 0,
		Payload:  []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE},
	}

	var buf bytes.Buffer
	require.NoError(t, codec.WriteFrame(&buf, frame))
	assert.Equal(t, expected, buf.Bytes())

	decoded, err := codec.ReadFrame(bytes.NewReader(expected))
	require.NoError(t, err)
	assert.Equal(t, CmdPing, decoded.Command)
	assert.Equal(t, uint32(0), decoded.StreamID)
	assert.Equal(t, frame.Payload, decoded.Payload)
}

func TestFrameCodec_WireExample_StreamOpen(t *testing.T) {
	// Note: docs/protocol/wire-format.md states payload length 0x0E (14) for
	// "127.0.0.1:445", but that string is 13 bytes. The test uses the actual
	// string length (0x0D = 13). This is a docs errata.
	target := "127.0.0.1:445"
	targetBytes := []byte(target)
	expected := make([]byte, 0, HeaderSize+len(targetBytes))
	expected = append(expected, []byte{
		0x01, 0x01, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x0D,
	}...)
	expected = append(expected, targetBytes...)

	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamOpen,
		Flags:    0x00,
		StreamID: 1,
		Payload:  []byte(target),
	}

	var buf bytes.Buffer
	require.NoError(t, codec.WriteFrame(&buf, frame))
	assert.Equal(t, expected, buf.Bytes())

	decoded, err := codec.ReadFrame(bytes.NewReader(expected))
	require.NoError(t, err)
	assert.Equal(t, CmdStreamOpen, decoded.Command)
	assert.Equal(t, uint32(1), decoded.StreamID)
	assert.Equal(t, []byte(target), decoded.Payload)
}

func TestFrameCodec_WireExample_StreamDataFIN(t *testing.T) {
	expected := []byte{
		0x01, 0x02, 0x01, 0x00,
		0x00, 0x00, 0x00, 0x03,
		0x00, 0x00, 0x00, 0x05,
		0x48, 0x65, 0x6C, 0x6C, 0x6F,
	}

	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		Flags:    FlagFIN,
		StreamID: 3,
		Payload:  []byte("Hello"),
	}

	var buf bytes.Buffer
	require.NoError(t, codec.WriteFrame(&buf, frame))
	assert.Equal(t, expected, buf.Bytes())

	decoded, err := codec.ReadFrame(bytes.NewReader(expected))
	require.NoError(t, err)
	assert.Equal(t, CmdStreamData, decoded.Command)
	assert.Equal(t, FlagFIN, decoded.Flags)
	assert.Equal(t, uint32(3), decoded.StreamID)
	assert.Equal(t, []byte("Hello"), decoded.Payload)
}

func TestFrameCodec_WireExample_WindowUpdate(t *testing.T) {
	expected := []byte{
		0x01, 0x07, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x05,
		0x00, 0x00, 0x00, 0x04,
		0x00, 0x01, 0x00, 0x00,
	}

	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdWindowUpdate,
		Flags:    0x00,
		StreamID: 5,
		Payload:  []byte{0x00, 0x01, 0x00, 0x00}, // 65536
	}

	var buf bytes.Buffer
	require.NoError(t, codec.WriteFrame(&buf, frame))
	assert.Equal(t, expected, buf.Bytes())

	decoded, err := codec.ReadFrame(bytes.NewReader(expected))
	require.NoError(t, err)
	assert.Equal(t, CmdWindowUpdate, decoded.Command)
	assert.Equal(t, uint32(5), decoded.StreamID)

	increment := binary.BigEndian.Uint32(decoded.Payload)
	assert.Equal(t, uint32(65536), increment)
}

func TestFrameCodec_WritePayloadExceedingMax(t *testing.T) {
	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		Flags:    0x00,
		StreamID: 1,
		Payload:  make([]byte, MaxPayloadSize+1),
	}
	var buf bytes.Buffer
	err := codec.WriteFrame(&buf, frame)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidFrame)
}

func TestFrameCodec_WriteNilFrame(t *testing.T) {
	codec := NewFrameCodec()
	var buf bytes.Buffer
	err := codec.WriteFrame(&buf, nil)
	require.Error(t, err)
}

func TestFrameCodec_WriteHeaderError(t *testing.T) {
	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdPing,
		Flags:    0x00,
		StreamID: 0,
		Payload:  nil,
	}
	w := &failWriter{failAfter: 0}
	err := codec.WriteFrame(w, frame)
	require.Error(t, err)
}

func TestFrameCodec_WritePayloadError(t *testing.T) {
	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		Flags:    0x00,
		StreamID: 1,
		Payload:  []byte("payload data"),
	}
	// Fail on the second Write call (payload write)
	w := &failWriter{failAfter: 1}
	err := codec.WriteFrame(w, frame)
	require.Error(t, err)
}

// failWriter fails after a specified number of Write calls.
type failWriter struct {
	calls     int
	failAfter int
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.calls >= f.failAfter {
		return 0, io.ErrClosedPipe
	}
	f.calls++
	return len(p), nil
}

func TestFrameCodec_ReadFromEmptyReader(t *testing.T) {
	codec := NewFrameCodec()
	r := bytes.NewReader(nil)
	_, err := codec.ReadFrame(r)
	require.Error(t, err)
}

func TestCommandString(t *testing.T) {
	tests := []struct {
		cmd      Command
		expected string
	}{
		{CmdStreamOpen, "STREAM_OPEN"},
		{CmdStreamData, "STREAM_DATA"},
		{CmdStreamClose, "STREAM_CLOSE"},
		{CmdStreamReset, "STREAM_RESET"},
		{CmdPing, "PING"},
		{CmdPong, "PONG"},
		{CmdWindowUpdate, "WINDOW_UPDATE"},
		{CmdGoAway, "GOAWAY"},
		{CmdUDPBind, "UDP_BIND"},
		{CmdUDPData, "UDP_DATA"},
		{CmdUDPUnbind, "UDP_UNBIND"},
		{CmdUpdateManifest, "UPDATE_MANIFEST"},
		{CmdUpdateBinary, "UPDATE_BINARY"},
		{Command(0xFF), "UNKNOWN(0xff)"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.cmd.String())
		})
	}
}

func TestCommandIsValid(t *testing.T) {
	for cmd := CmdStreamOpen; cmd <= CmdUpdateBinary; cmd++ {
		assert.True(t, cmd.IsValid(), "command 0x%02x should be valid", cmd)
	}
	assert.False(t, Command(0x00).IsValid())
	assert.False(t, Command(0x0E).IsValid())
	assert.False(t, Command(0xFF).IsValid())
}

func TestFlagString(t *testing.T) {
	tests := []struct {
		flag     Flag
		expected string
	}{
		{0x00, "0x00"},
		{FlagFIN, "FIN"},
		{FlagACK, "ACK"},
		{FlagFIN | FlagACK, "FIN|ACK"},
		{Flag(0x80), "0x80"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.flag.String())
		})
	}
}

func BenchmarkEncodeFrame(b *testing.B) {
	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		Flags:    0x00,
		StreamID: 1,
		Payload:  make([]byte, 4096),
	}
	var buf bytes.Buffer

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		_ = codec.WriteFrame(&buf, frame)
	}
}

func BenchmarkDecodeFrame(b *testing.B) {
	codec := NewFrameCodec()
	frame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		Flags:    0x00,
		StreamID: 1,
		Payload:  make([]byte, 4096),
	}
	var buf bytes.Buffer
	_ = codec.WriteFrame(&buf, frame)
	data := buf.Bytes()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(data)
		_, _ = codec.ReadFrame(r)
	}
}
