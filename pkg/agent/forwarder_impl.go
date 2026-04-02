package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

const defaultBufferSize = 32 * 1024 // 32KB

// Forwarder copies data bidirectionally between a multiplexed stream and
// a local TCP service.
type Forwarder struct {
	config ServiceForwarderConfig
	logger *slog.Logger
}

// Compile-time interface check.
var _ ServiceForwarder = (*Forwarder)(nil)

// NewForwarder creates a Forwarder with the given config.
func NewForwarder(cfg ServiceForwarderConfig, logger *slog.Logger) *Forwarder {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = defaultBufferSize
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	return &Forwarder{config: cfg, logger: logger}
}

// Forward dials the target, then copies data in both directions until
// one side closes or ctx is canceled.
func (f *Forwarder) Forward(
	ctx context.Context,
	stream protocol.Stream,
	target string,
) error {
	dialer := &net.Dialer{Timeout: f.config.DialTimeout}
	rawLocal, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return fmt.Errorf("forwarder: dial %s: %w", target, err)
	}
	local := newIdleConn(rawLocal, f.config.IdleTimeout)

	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	setErr := func(e error) {
		errOnce.Do(func() { firstErr = e })
	}

	// Close both ends when context is canceled to unblock io.Copy.
	// Use Reset (not Close) on the stream to force-unblock Read.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		local.Close()
		if ss, ok := stream.(*protocol.StreamSession); ok {
			ss.Reset(0)
		} else {
			stream.Close()
		}
	}()

	// stream -> local
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, f.config.BufferSize)
		_, cpErr := io.CopyBuffer(local, stream, buf)
		if cpErr != nil && ctx.Err() == nil {
			setErr(cpErr)
		}
		// Half-close the write side to signal EOF to the local service.
		if tc, ok := rawLocal.(*net.TCPConn); ok {
			tc.CloseWrite() //nolint:errcheck // best-effort half-close
		}
	}()

	// local -> stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, f.config.BufferSize)
		_, cpErr := io.CopyBuffer(stream, local, buf)
		if cpErr != nil && ctx.Err() == nil {
			setErr(cpErr)
		}
	}()

	wg.Wait()
	return firstErr
}
