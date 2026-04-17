package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// FuzzReadFrame feeds random bytes into the frame decoder.
// It should never panic -- only return errors for invalid input.
func FuzzReadFrame(f *testing.F) {
	codec := NewFrameCodec()

	// Seed corpus: valid frames for each command
	for _, cmd := range []Command{
		CmdStreamOpen, CmdStreamData, CmdStreamClose, CmdStreamReset,
		CmdPing, CmdPong, CmdWindowUpdate, CmdGoAway,
		CmdUDPBind, CmdUDPData, CmdUDPUnbind,
		CmdUpdateManifest, CmdUpdateBinary,
		CmdServiceList,
	} {
		frame := validFrame(cmd, 0, nil)
		f.Add(frame)
	}

	// Seed: SERVICE_LIST payload with newline-separated services
	f.Add(validFrame(CmdServiceList, 0, []byte("samba\nhttp\napi")))

	// Seed: frame with payload
	f.Add(validFrame(CmdStreamData, 1, []byte("hello")))

	// Seed: frame with FIN flag
	f.Add(validFrameWithFlags(CmdStreamData, FlagFIN, 1, []byte("fin")))

	// Seed: frame with ACK flag
	f.Add(validFrameWithFlags(CmdStreamOpen, FlagACK, 1, nil))

	// Seed: frame with max stream ID
	f.Add(validFrame(CmdStreamOpen, 0xFFFFFFFF, nil))

	// Seed: empty input
	f.Add([]byte{})

	// Seed: truncated header
	f.Add([]byte{0x01, 0x01, 0x00, 0x00})

	// Seed: invalid version
	f.Add(validFrameWithVersion(0xFF, CmdPing, 0, nil))

	// Seed: large payload length (but truncated data)
	hdr := make([]byte, HeaderSize)
	hdr[0] = ProtocolVersion
	hdr[1] = byte(CmdStreamData)
	binary.BigEndian.PutUint32(hdr[8:12], 1024)
	f.Add(hdr)

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		// Must not panic. Errors are expected for invalid input.
		frame, err := codec.ReadFrame(r)
		if err != nil {
			return
		}

		// If we got a valid frame, round-trip it
		var buf bytes.Buffer
		if writeErr := codec.WriteFrame(&buf, frame); writeErr != nil {
			return
		}

		// Decode the round-tripped frame
		frame2, err := codec.ReadFrame(&buf)
		if err != nil {
			t.Fatalf("round-trip decode failed: %v", err)
		}

		// Verify round-trip integrity
		if frame.Version != frame2.Version {
			t.Fatalf("version mismatch: %d vs %d", frame.Version, frame2.Version)
		}
		if frame.Command != frame2.Command {
			t.Fatalf("command mismatch: %d vs %d", frame.Command, frame2.Command)
		}
		if frame.Flags != frame2.Flags {
			t.Fatalf("flags mismatch: %d vs %d", frame.Flags, frame2.Flags)
		}
		if frame.StreamID != frame2.StreamID {
			t.Fatalf("stream ID mismatch: %d vs %d", frame.StreamID, frame2.StreamID)
		}
		if !bytes.Equal(frame.Payload, frame2.Payload) {
			t.Fatalf("payload mismatch")
		}
	})
}

// FuzzParseUDPDataPayload feeds random bytes into the UDP payload parser.
func FuzzParseUDPDataPayload(f *testing.F) {
	// Seed: valid UDP data payload (1 byte addr len + addr + data)
	f.Add([]byte{5, 'h', 'e', 'l', 'l', 'o', 'd', 'a', 't', 'a'})
	f.Add([]byte{0}) // zero-length addr
	f.Add([]byte{})  // empty

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic
		dgram, err := ParseUDPDataPayload(data)
		if err != nil {
			return
		}

		// If valid, round-trip it
		rebuilt, err := BuildUDPDataPayload(dgram.SourceAddr, dgram.Payload)
		if err != nil {
			return
		}

		dgram2, err := ParseUDPDataPayload(rebuilt)
		if err != nil {
			t.Fatalf("round-trip parse failed: %v", err)
		}
		if dgram.SourceAddr != dgram2.SourceAddr {
			t.Fatalf("addr mismatch: %q vs %q", dgram.SourceAddr, dgram2.SourceAddr)
		}
		if !bytes.Equal(dgram.Payload, dgram2.Payload) {
			t.Fatalf("payload mismatch")
		}
	})
}

// --- Seed helpers ---

func validFrame(cmd Command, streamID uint32, payload []byte) []byte {
	return validFrameWithFlags(cmd, 0, streamID, payload)
}

func validFrameWithFlags(cmd Command, flags Flag, streamID uint32, payload []byte) []byte {
	return buildRawFrame(ProtocolVersion, cmd, flags, streamID, payload)
}

func validFrameWithVersion(version byte, cmd Command, streamID uint32, payload []byte) []byte {
	return buildRawFrame(version, cmd, 0, streamID, payload)
}

func buildRawFrame(version byte, cmd Command, flags Flag, streamID uint32, payload []byte) []byte {
	hdr := make([]byte, HeaderSize, HeaderSize+len(payload))
	hdr[0] = version
	hdr[1] = byte(cmd)
	hdr[2] = byte(flags)
	hdr[3] = 0x00
	binary.BigEndian.PutUint32(hdr[4:8], streamID)
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(payload))) //nolint:gosec // test only
	return append(hdr, payload...)
}
