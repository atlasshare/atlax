package protocol

import (
	"errors"
	"fmt"
)

// Error represents a wire protocol error tied to a specific stream.
type Error struct {
	Code     uint32
	Message  string
	StreamID uint32
}

// Error returns a human-readable representation of the protocol error.
func (e *Error) Error() string {
	return fmt.Sprintf("protocol error on stream %d (code %d): %s", e.StreamID, e.Code, e.Message)
}

// Sentinel errors for common protocol-level failure conditions.
var (
	ErrStreamClosed       = errors.New("stream is closed")
	ErrWindowExhausted    = errors.New("flow-control window exhausted")
	ErrMaxStreamsExceeded = errors.New("maximum concurrent streams exceeded")
	ErrInvalidFrame       = errors.New("invalid frame")
	ErrGoAway             = errors.New("received GOAWAY from peer")
)
