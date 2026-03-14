package protocol

import (
	"context"
	"time"
)

// Muxer multiplexes many logical streams over a single transport connection.
type Muxer interface {
	// OpenStream initiates a new stream to the remote peer.
	OpenStream(ctx context.Context) (Stream, error)

	// AcceptStream waits for the remote peer to open a new stream.
	AcceptStream(ctx context.Context) (Stream, error)

	// Close tears down all streams and the underlying connection.
	Close() error

	// GoAway signals the remote peer that no new streams will be accepted.
	GoAway(code uint32) error

	// Ping measures round-trip latency to the remote peer.
	Ping(ctx context.Context) (time.Duration, error)

	// NumStreams returns the count of currently open streams.
	NumStreams() int
}

// MuxConfig holds tunables for the multiplexer.
type MuxConfig struct {
	MaxConcurrentStreams int
	InitialStreamWindow  int
	ConnectionWindow     int
	PingInterval         time.Duration
	PingTimeout          time.Duration
	IdleTimeout          time.Duration
}
