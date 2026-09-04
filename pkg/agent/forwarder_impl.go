package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

const (
	defaultBufferSize    = 32 * 1024 // 32KB
	defaultLingerTimeout = 30 * time.Second
)

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
	if cfg.LingerTimeout <= 0 {
		cfg.LingerTimeout = defaultLingerTimeout
	}
	return &Forwarder{config: cfg, logger: logger}
}

// Forward dials the target, then copies data in both directions. It
// returns once both directions have finished, the context is canceled,
// or the linger timeout expires after one direction has already closed.
// The local socket is always released before returning.
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
	defer local.Close()

	link := &forwardLink{stream: stream, local: local, raw: rawLocal}
	done := make(chan error, 2)
	go link.copyStreamToLocal(f.config.BufferSize, done)
	go link.copyLocalToStream(f.config.BufferSize, done)

	return f.await(ctx, link, done)
}

// await collects both copy results. The first direction may take as
// long as it needs; the second is bounded by LingerTimeout so a peer
// that never answers a half-close cannot pin the stream open forever.
func (f *Forwarder) await(ctx context.Context, link *forwardLink, done <-chan error) error {
	aborted, first := waitDirection(ctx, link, done, nil)
	if aborted {
		<-done
		return nil
	}

	linger := time.NewTimer(f.config.LingerTimeout)
	defer linger.Stop()
	aborted, second := waitDirection(ctx, link, done, linger.C)
	if aborted {
		return first
	}
	if first != nil {
		return first
	}
	return second
}

// waitDirection waits for one copy direction to finish. If the context
// is canceled or the deadline fires first, both ends are torn down and
// the caller is told the result is not a genuine transfer error.
func waitDirection(
	ctx context.Context,
	link *forwardLink,
	done <-chan error,
	deadline <-chan time.Time,
) (aborted bool, err error) {
	select {
	case err = <-done:
		return false, err
	case <-deadline:
	case <-ctx.Done():
	}
	link.abort()
	<-done
	return true, nil
}

// forwardLink owns the two endpoints of one forwarded connection.
type forwardLink struct {
	stream protocol.Stream
	local  net.Conn
	raw    net.Conn

	abortOnce sync.Once
	aborted   atomic.Bool
}

// copyStreamToLocal relays relay-to-agent bytes into the local service.
// When the stream reaches EOF the local write side is half-closed so
// the service sees end of request.
func (l *forwardLink) copyStreamToLocal(bufSize int, done chan<- error) {
	buf := make([]byte, bufSize)
	_, err := io.CopyBuffer(l.local, l.stream, buf)
	if tc, ok := l.raw.(*net.TCPConn); ok {
		tc.CloseWrite() //nolint:errcheck // best-effort half-close
	}
	done <- l.filter(err)
}

// copyLocalToStream relays local service bytes toward the relay. When
// the service closes its side, the stream is half-closed so the relay
// learns the connection is finished instead of holding a dead stream.
func (l *forwardLink) copyLocalToStream(bufSize int, done chan<- error) {
	buf := make([]byte, bufSize)
	_, err := io.CopyBuffer(l.stream, l.local, buf)
	l.stream.Close() //nolint:errcheck // half-close is best-effort
	done <- l.filter(err)
}

// abort force-closes both ends exactly once so blocked copies unwind.
func (l *forwardLink) abort() {
	l.abortOnce.Do(func() {
		l.aborted.Store(true)
		l.local.Close()
		if ss, ok := l.stream.(*protocol.StreamSession); ok {
			ss.Reset(0)
			return
		}
		l.stream.Close() //nolint:errcheck // best-effort
	})
}

// filter drops errors caused by our own abort; those are not faults.
func (l *forwardLink) filter(err error) error {
	if err == nil || l.aborted.Load() {
		return nil
	}
	return err
}
