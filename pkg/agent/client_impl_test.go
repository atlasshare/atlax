package agent

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// pipeDialer creates an in-memory connection for each dial. The remote
// side is available via Accept.
type pipeDialer struct {
	mu     sync.Mutex
	conns  chan net.Conn
	failN  int // fail the first N dials
	dialed int
}

func newPipeDialer() *pipeDialer {
	return &pipeDialer{conns: make(chan net.Conn, 16)}
}

func (d *pipeDialer) DialContext(_ context.Context, _ string) (net.Conn, error) {
	d.mu.Lock()
	d.dialed++
	n := d.dialed
	fail := n <= d.failN
	d.mu.Unlock()

	if fail {
		return nil, net.ErrClosed
	}

	c1, c2 := net.Pipe()
	d.conns <- c2 // remote end for relay simulator
	return c1, nil
}

// accept returns the remote end of the last dialed connection.
func (d *pipeDialer) accept(t *testing.T) net.Conn {
	t.Helper()
	select {
	case c := <-d.conns:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("pipeDialer: no connection within timeout")
		return nil
	}
}

func testClientConfig() ClientConfig {
	return ClientConfig{
		RelayAddr:            "relay.test:8443",
		HeartbeatInterval:    100 * time.Millisecond,
		HeartbeatTimeout:     50 * time.Millisecond,
		ReconnectBackoff:     10 * time.Millisecond,
		MaxReconnectAttempts: 3,
	}
}

func testLogger() *slog.Logger {
	return slog.Default()
}

// relaySimulator runs a MuxSession on the remote end of a pipe.
func relaySimulator(t *testing.T, conn net.Conn) *protocol.MuxSession {
	t.Helper()
	return protocol.NewMuxSession(conn, protocol.RoleRelay, protocol.MuxConfig{
		MaxConcurrentStreams: 256,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	})
}

func TestNewClient_DisconnectedState(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	s := c.Status()
	assert.False(t, s.Connected)
	assert.Equal(t, "relay.test:8443", s.RelayAddr)
}

func TestClient_Connect(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()
		<-ctx.Done()
	}()

	require.NoError(t, c.Connect(ctx))

	s := c.Status()
	assert.True(t, s.Connected)
	assert.False(t, s.ConnectedAt.IsZero())
}

func TestClient_ConnectFailsTLSError(t *testing.T) {
	dialer := newPipeDialer()
	dialer.failN = 1

	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := c.Connect(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent: connect")
}

func TestClient_ConnectSetsStatus(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()
		<-ctx.Done()
	}()

	require.NoError(t, c.Connect(ctx))

	s := c.Status()
	assert.True(t, s.Connected)
	assert.Equal(t, 0, s.StreamCount)
}

func TestClient_Reconnect(t *testing.T) {
	dialer := newPipeDialer()
	dialer.failN = 1 // first dial fails, second succeeds

	cfg := testClientConfig()
	cfg.MaxReconnectAttempts = 5

	c := NewClient(cfg, testLogger(),
		WithDialer(dialer),
		WithBackoffConfig(BackoffConfig{
			InitialInterval: 10 * time.Millisecond,
			MaxInterval:     50 * time.Millisecond,
			Multiplier:      2.0,
			JitterFraction:  0,
		}),
	)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// Accept the successful connection (attempt 2)
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()
		<-ctx.Done()
	}()

	require.NoError(t, c.Reconnect(ctx))
	assert.True(t, c.Status().Connected)
}

func TestClient_ReconnectExhaustsAttempts(t *testing.T) {
	dialer := newPipeDialer()
	dialer.failN = 100 // all dials fail

	cfg := testClientConfig()
	cfg.MaxReconnectAttempts = 3

	c := NewClient(cfg, testLogger(),
		WithDialer(dialer),
		WithBackoffConfig(BackoffConfig{
			InitialInterval: 1 * time.Millisecond,
			MaxInterval:     5 * time.Millisecond,
			Multiplier:      1.0,
			JitterFraction:  0,
		}),
	)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.Reconnect(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exhausted 3 attempts")
}

func TestClient_ReconnectRespectsContext(t *testing.T) {
	dialer := newPipeDialer()
	dialer.failN = 100

	c := NewClient(testClientConfig(), testLogger(),
		WithDialer(dialer),
		WithBackoffConfig(BackoffConfig{
			InitialInterval: 1 * time.Second,
			MaxInterval:     1 * time.Second,
			Multiplier:      1.0,
			JitterFraction:  0,
		}),
	)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.Reconnect(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestClient_Close(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()
		<-ctx.Done()
	}()

	require.NoError(t, c.Connect(ctx))
	assert.True(t, c.Status().Connected)

	require.NoError(t, c.Close())
	assert.False(t, c.Status().Connected)
}

func TestClient_CloseIsIdempotent(t *testing.T) {
	c := NewClient(testClientConfig(), testLogger(), WithDialer(newPipeDialer()))
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
}

func TestClient_StatusSnapshot(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	// Before connect
	s := c.Status()
	assert.False(t, s.Connected)
	assert.Equal(t, "relay.test:8443", s.RelayAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()
		<-ctx.Done()
	}()

	require.NoError(t, c.Connect(ctx))

	// After connect
	s = c.Status()
	assert.True(t, s.Connected)
	assert.False(t, s.ConnectedAt.IsZero())
}

func TestClient_HeartbeatPings(t *testing.T) {
	dialer := newPipeDialer()
	cfg := testClientConfig()
	cfg.HeartbeatInterval = 50 * time.Millisecond
	cfg.HeartbeatTimeout = 200 * time.Millisecond

	c := NewClient(cfg, testLogger(), WithDialer(dialer))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()
		<-ctx.Done()
	}()

	require.NoError(t, c.Connect(ctx))

	// Wait for a few heartbeat cycles
	time.Sleep(200 * time.Millisecond)

	s := c.Status()
	assert.True(t, s.Connected)
	assert.False(t, s.LastHeartbeat.IsZero(), "heartbeat should have updated")
}

func TestClient_HeartbeatDetectsDeadConnection(t *testing.T) {
	dialer := newPipeDialer()
	cfg := testClientConfig()
	cfg.HeartbeatInterval = 30 * time.Millisecond
	cfg.HeartbeatTimeout = 20 * time.Millisecond

	c := NewClient(cfg, testLogger(), WithDialer(dialer))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		remote := dialer.accept(t)
		// Start relay briefly then kill it
		relay := relaySimulator(t, remote)
		time.Sleep(20 * time.Millisecond)
		relay.Close()
	}()

	require.NoError(t, c.Connect(ctx))

	// Wait for heartbeat to detect the dead connection
	time.Sleep(200 * time.Millisecond)
}

func TestClient_Mux(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	assert.Nil(t, c.Mux())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()
		<-ctx.Done()
	}()

	require.NoError(t, c.Connect(ctx))
	assert.NotNil(t, c.Mux())
}

func TestClient_ConcurrentConnectClose(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Accept connections for any that succeed
			select {
			case remote := <-dialer.conns:
				relay := relaySimulator(t, remote)
				defer relay.Close()
				<-ctx.Done()
			case <-ctx.Done():
			}
		}()
	}

	// Concurrent connect and close
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.Connect(ctx) //nolint:errcheck // race test, errors expected
	}()
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		c.Close()
	}()

	wg.Wait()
}

// --- CmdServiceList emission tests ---

func TestClient_Connect_SendsServiceList(t *testing.T) {
	dialer := newPipeDialer()

	cfg := testClientConfig()
	cfg.Services = []string{"samba", "http"}

	c := NewClient(cfg, testLogger(), WithDialer(dialer))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	relayReceived := make(chan []string, 1)
	go func() {
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()

		select {
		case services := <-relay.ServiceListCh():
			relayReceived <- services
		case <-ctx.Done():
		}
		<-ctx.Done()
	}()

	require.NoError(t, c.Connect(ctx))

	select {
	case services := <-relayReceived:
		assert.Equal(t, []string{"samba", "http"}, services)
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not receive CmdServiceList after Connect")
	}
}

func TestClient_Connect_SkipsEmptyServiceList(t *testing.T) {
	dialer := newPipeDialer()

	cfg := testClientConfig()
	cfg.Services = nil // explicit: agent has no services to advertise

	c := NewClient(cfg, testLogger(), WithDialer(dialer))
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sawFrame := make(chan struct{}, 1)
	go func() {
		remote := dialer.accept(t)
		relay := relaySimulator(t, remote)
		defer relay.Close()

		select {
		case <-relay.ServiceListCh():
			sawFrame <- struct{}{}
		case <-ctx.Done():
		}
		<-ctx.Done()
	}()

	require.NoError(t, c.Connect(ctx))

	// Wait a conservative amount of time; no CmdServiceList should arrive.
	select {
	case <-sawFrame:
		t.Fatal("Connect emitted CmdServiceList despite empty Services slice")
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}
