package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

// FrameCodec encodes and decodes wire protocol frames. It satisfies both
// the FrameReader and FrameWriter interfaces.
type FrameCodec struct{}

// NewFrameCodec returns a new FrameCodec.
func NewFrameCodec() *FrameCodec {
	return &FrameCodec{}
}

// Compile-time interface checks.
var (
	_ FrameReader = (*FrameCodec)(nil)
	_ FrameWriter = (*FrameCodec)(nil)
)

// ReadFrame reads a single frame from r. It reads the 12-byte header,
// validates it, then reads the payload.
func (c *FrameCodec) ReadFrame(r io.Reader) (*Frame, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("frame: read header: %w", err)
	}

	if err := c.validateHeader(hdr); err != nil {
		return nil, err
	}

	payloadLen := binary.BigEndian.Uint32(hdr[8:12])

	frame := &Frame{
		Version:  hdr[0],
		Command:  Command(hdr[1]),
		Flags:    Flag(hdr[2]),
		StreamID: binary.BigEndian.Uint32(hdr[4:8]),
	}

	if payloadLen > 0 {
		frame.Payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(r, frame.Payload); err != nil {
			return nil, fmt.Errorf("frame: read payload: %w", err)
		}
	}

	return frame, nil
}

// WriteFrame encodes a frame and writes it to w. The Reserved byte is
// always set to 0x00 regardless of the frame's fields.
func (c *FrameCodec) WriteFrame(w io.Writer, f *Frame) error {
	if f == nil {
		return fmt.Errorf("frame: write: %w: nil frame", ErrInvalidFrame)
	}

	n := len(f.Payload)
	if n > MaxPayloadSize {
		return fmt.Errorf("frame: write: %w: payload size %d exceeds max %d",
			ErrInvalidFrame, n, MaxPayloadSize)
	}
	payloadLen := uint32(n) //nolint:gosec // n <= MaxPayloadSize (16MB), fits uint32

	var hdr [HeaderSize]byte
	hdr[0] = f.Version
	hdr[1] = byte(f.Command)
	hdr[2] = byte(f.Flags)
	hdr[3] = 0x00 // reserved
	binary.BigEndian.PutUint32(hdr[4:8], f.StreamID)
	binary.BigEndian.PutUint32(hdr[8:12], payloadLen)

	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("frame: write header: %w", err)
	}

	if payloadLen > 0 {
		if _, err := w.Write(f.Payload); err != nil {
			return fmt.Errorf("frame: write payload: %w", err)
		}
	}

	return nil
}

// validateHeader checks the header for protocol violations.
func (c *FrameCodec) validateHeader(hdr [HeaderSize]byte) error {
	if hdr[0] != ProtocolVersion {
		return fmt.Errorf("frame: %w: unsupported version 0x%02x",
			ErrInvalidFrame, hdr[0])
	}

	payloadLen := binary.BigEndian.Uint32(hdr[8:12])
	if payloadLen > MaxPayloadSize {
		return fmt.Errorf("frame: %w: payload length %d exceeds max %d",
			ErrInvalidFrame, payloadLen, MaxPayloadSize)
	}

	return nil
}
