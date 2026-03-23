package protocol

import (
	"fmt"
	"io"
)

// Protocol-level constants define the wire format boundaries.
const (
	// ProtocolVersion is the current version of the atlax wire protocol.
	ProtocolVersion byte = 0x01

	// HeaderSize is the fixed size in bytes of every frame header.
	HeaderSize = 12

	// MaxPayloadSize is the maximum allowed payload size per frame (16 MB).
	MaxPayloadSize = 16 * 1024 * 1024
)

// Command identifies the type of a wire protocol frame.
type Command byte

const (
	CmdStreamOpen   Command = 0x01
	CmdStreamData   Command = 0x02
	CmdStreamClose  Command = 0x03
	CmdStreamReset  Command = 0x04
	CmdPing         Command = 0x05
	CmdPong         Command = 0x06
	CmdWindowUpdate Command = 0x07
	CmdGoAway       Command = 0x08
	CmdUDPBind      Command = 0x09
	CmdUDPData      Command = 0x0A
	CmdUDPUnbind    Command = 0x0B
)

// commandNames maps valid commands to their wire protocol names.
var commandNames = map[Command]string{
	CmdStreamOpen:   "STREAM_OPEN",
	CmdStreamData:   "STREAM_DATA",
	CmdStreamClose:  "STREAM_CLOSE",
	CmdStreamReset:  "STREAM_RESET",
	CmdPing:         "PING",
	CmdPong:         "PONG",
	CmdWindowUpdate: "WINDOW_UPDATE",
	CmdGoAway:       "GOAWAY",
	CmdUDPBind:      "UDP_BIND",
	CmdUDPData:      "UDP_DATA",
	CmdUDPUnbind:    "UDP_UNBIND",
}

// String returns the human-readable name of the command.
func (c Command) String() string {
	if name, ok := commandNames[c]; ok {
		return name
	}
	return fmt.Sprintf("UNKNOWN(0x%02x)", byte(c))
}

// IsValid reports whether c is a defined protocol command.
func (c Command) IsValid() bool {
	_, ok := commandNames[c]
	return ok
}

// Flag is a bitfield attached to each frame for signaling conditions.
type Flag byte

const (
	FlagFIN Flag = 1 << 0
	FlagACK Flag = 1 << 1
)

// String returns a human-readable representation of the flag bitfield.
func (f Flag) String() string {
	switch f {
	case 0:
		return "0x00"
	case FlagFIN:
		return "FIN"
	case FlagACK:
		return "ACK"
	case FlagFIN | FlagACK:
		return "FIN|ACK"
	default:
		return fmt.Sprintf("0x%02x", byte(f))
	}
}

// Frame represents a single wire protocol frame with its header fields and
// optional payload.
type Frame struct {
	Version  byte
	Command  Command
	Flags    Flag
	StreamID uint32
	Payload  []byte
}

// FrameReader reads a single frame from the underlying reader.
type FrameReader interface {
	ReadFrame(r io.Reader) (*Frame, error)
}

// FrameWriter writes a single frame to the underlying writer.
type FrameWriter interface {
	WriteFrame(w io.Writer, f *Frame) error
}
