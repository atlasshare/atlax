package agent

import (
	"context"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// ServiceForwarder copies data between a multiplexed stream and a local
// network service.
type ServiceForwarder interface {
	// Forward connects the given stream to the target address and copies
	// data bidirectionally until one side closes or an error occurs.
	Forward(ctx context.Context, stream protocol.Stream, target string) error
}

// ServiceForwarderConfig holds tunables for local service forwarding.
type ServiceForwarderConfig struct {
	DialTimeout time.Duration
	IdleTimeout time.Duration
	BufferSize  int
	// LingerTimeout bounds how long Forward waits for the peer to close
	// its half of the connection after the other half has already closed.
	// When it expires the stream is reset and the local socket released.
	LingerTimeout time.Duration
}
