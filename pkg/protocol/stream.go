package protocol

// StreamState represents the lifecycle state of a multiplexed stream.
type StreamState int

const (
	StateOpen       StreamState = 0
	StateHalfClosed StreamState = 1
	StateClosed     StreamState = 2
	StateReset      StreamState = 3
)

// Stream is a bidirectional byte stream multiplexed over a single connection.
type Stream interface {
	// ID returns the unique stream identifier.
	ID() uint32

	// State returns the current lifecycle state of the stream.
	State() StreamState

	// Read reads up to len(p) bytes from the stream into p.
	Read(p []byte) (int, error)

	// Write writes len(p) bytes from p to the stream.
	Write(p []byte) (int, error)

	// Close gracefully closes the stream.
	Close() error

	// ReceiveWindow returns the number of bytes the peer is allowed to send
	// before a window update is required.
	ReceiveWindow() int
}

// StreamConfig holds tunables for individual stream behavior.
type StreamConfig struct {
	InitialWindowSize int
	MaxFrameSize      int
}
