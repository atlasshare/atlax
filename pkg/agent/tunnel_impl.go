package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// TunnelRunner orchestrates stream acceptance and forwarding for an agent.
type TunnelRunner struct {
	client   Client
	fwdCfg   ServiceForwarderConfig
	services map[string]string // service name -> local addr
	logger   *slog.Logger

	mu            sync.Mutex
	activeStreams int32
	totalStreams  atomic.Int64
	startTime     time.Time
	cancelAccept  context.CancelFunc
	acceptStopped chan struct{}
}

// Compile-time interface check.
var _ Tunnel = (*TunnelRunner)(nil)

// NewTunnel creates a TunnelRunner that accepts streams from the client's
// MuxSession and forwards them to the specified local services.
func NewTunnel(
	client Client,
	fwdCfg ServiceForwarderConfig,
	services []ServiceMapping,
	logger *slog.Logger,
) *TunnelRunner {
	svcMap := make(map[string]string, len(services))
	for _, s := range services {
		svcMap[s.Name] = s.LocalAddr
	}
	return &TunnelRunner{
		client:   client,
		fwdCfg:   fwdCfg,
		services: svcMap,
		logger:   logger,
	}
}

// Start begins accepting streams from the relay and forwarding them
// to local services. Blocks until ctx is canceled or an error occurs.
func (t *TunnelRunner) Start(ctx context.Context) error {
	acceptCtx, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.cancelAccept = cancel
	t.startTime = time.Now()
	t.acceptStopped = make(chan struct{})
	t.mu.Unlock()

	defer close(t.acceptStopped)
	defer cancel()

	tc, ok := t.client.(*TunnelClient)
	if !ok {
		return fmt.Errorf("tunnel: client does not expose Mux()")
	}

	mux := tc.Mux()
	if mux == nil {
		return fmt.Errorf("tunnel: client not connected")
	}

	fwd := NewForwarder(t.fwdCfg, t.logger)

	for {
		stream, err := mux.AcceptStream(acceptCtx)
		if err != nil {
			if acceptCtx.Err() != nil {
				return nil // clean shutdown
			}
			return fmt.Errorf("tunnel: accept stream: %w", err)
		}

		target := t.resolveTarget(stream)
		if target == "" {
			t.logger.Warn("tunnel: no service mapping for stream",
				"stream_id", stream.ID())
			if ss, streamOk := stream.(*protocol.StreamSession); streamOk {
				ss.Reset(1) // error code 1: no such service
			}
			continue
		}

		atomic.AddInt32(&t.activeStreams, 1)
		t.totalStreams.Add(1)

		go func() {
			defer atomic.AddInt32(&t.activeStreams, -1)
			if fwdErr := fwd.Forward(acceptCtx, stream, target); fwdErr != nil {
				t.logger.Warn("tunnel: forward error",
					"stream_id", stream.ID(),
					"target", target,
					"error", fwdErr)
			}
		}()
	}
}

// Stop cancels the accept loop and waits for it to finish.
func (t *TunnelRunner) Stop(ctx context.Context) error {
	t.mu.Lock()
	cancel := t.cancelAccept
	stopped := t.acceptStopped
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if stopped != nil {
		select {
		case <-stopped:
		case <-ctx.Done():
			return fmt.Errorf("tunnel: stop: %w", ctx.Err())
		}
	}
	return nil
}

// Stats returns a snapshot of tunnel activity.
func (t *TunnelRunner) Stats() TunnelStats {
	t.mu.Lock()
	start := t.startTime
	t.mu.Unlock()

	var uptime time.Duration
	if !start.IsZero() {
		uptime = time.Since(start)
	}

	return TunnelStats{
		ActiveStreams: int(atomic.LoadInt32(&t.activeStreams)),
		TotalStreams:  t.totalStreams.Load(),
		Uptime:        uptime,
	}
}

// resolveTarget maps a stream to a local service address by reading
// the STREAM_OPEN payload for the service name. Falls back to the sole
// configured service if only one exists.
func (t *TunnelRunner) resolveTarget(stream protocol.Stream) string {
	// Try to read service name from STREAM_OPEN payload.
	if ss, ok := stream.(*protocol.StreamSession); ok {
		if payload := ss.OpenPayload(); len(payload) > 0 {
			if addr, found := t.services[string(payload)]; found {
				return addr
			}
		}
	}

	// Single-service fallback: if exactly one service is configured,
	// route all streams there regardless of payload.
	if len(t.services) == 1 {
		for _, addr := range t.services {
			return addr
		}
	}

	return ""
}
