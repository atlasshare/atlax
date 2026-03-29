package agent

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/protocol"
)

func TestNewTunnel(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	tun := NewTunnel(c, ServiceForwarderConfig{}, []ServiceMapping{
		{Name: "samba", LocalAddr: "127.0.0.1:445"},
	}, slog.Default())

	assert.NotNil(t, tun)
	assert.Equal(t, 0, tun.Stats().ActiveStreams)
}

func TestTunnel_StartAcceptsAndForwards(t *testing.T) {
	// Start an echo server as the local service
	echo := echoServerForTunnel(t)

	// Set up agent client with pipe dialer
	dialer := newPipeDialer()
	cfg := testClientConfig()
	c := NewClient(cfg, testLogger(), WithDialer(dialer))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Accept the connection on the relay side
	relayCh := make(chan *protocol.MuxSession, 1)
	go func() {
		remote := dialer.accept(t)
		relay := protocol.NewMuxSession(remote, protocol.RoleRelay, protocol.MuxConfig{
			MaxConcurrentStreams: 256,
			InitialStreamWindow:  262144,
			ConnectionWindow:     1048576,
			PingInterval:         30 * time.Second,
			PingTimeout:          5 * time.Second,
			IdleTimeout:          60 * time.Second,
		})
		relayCh <- relay
	}()

	require.NoError(t, c.Connect(ctx))
	relay := <-relayCh
	defer relay.Close()
	defer c.Close()

	// Create tunnel with single service pointing to echo server
	tun := NewTunnel(c, ServiceForwarderConfig{
		DialTimeout: 2 * time.Second,
		BufferSize:  1024,
	}, []ServiceMapping{
		{Name: "echo", LocalAddr: echo.Addr().String()},
	}, slog.Default())

	// Start tunnel in background
	tunnelDone := make(chan error, 1)
	go func() {
		tunnelDone <- tun.Start(ctx)
	}()

	// Give tunnel time to start accepting
	time.Sleep(50 * time.Millisecond)

	// Relay opens a stream to the agent
	stream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	// Write data through the stream, should echo back
	msg := []byte("hello through tunnel")
	_, err = stream.Write(msg)
	require.NoError(t, err)

	buf := make([]byte, 64)
	n, err := stream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, string(msg), string(buf[:n]))

	// Check stats
	stats := tun.Stats()
	assert.GreaterOrEqual(t, int(stats.TotalStreams), 1)

	// Stop tunnel
	cancel()
	<-tunnelDone
}

func TestTunnel_StopRespectsContext(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	tun := NewTunnel(c, ServiceForwarderConfig{}, []ServiceMapping{
		{Name: "samba", LocalAddr: "127.0.0.1:445"},
	}, slog.Default())

	// Stop without Start should not block
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stopCancel()
	require.NoError(t, tun.Stop(stopCtx))
}

func TestTunnel_Stats(t *testing.T) {
	dialer := newPipeDialer()
	c := NewClient(testClientConfig(), testLogger(), WithDialer(dialer))
	defer c.Close()

	tun := NewTunnel(c, ServiceForwarderConfig{}, nil, slog.Default())

	stats := tun.Stats()
	assert.Equal(t, 0, stats.ActiveStreams)
	assert.Equal(t, int64(0), stats.TotalStreams)
	assert.Equal(t, time.Duration(0), stats.Uptime)
}

// echoServerForTunnel starts a TCP echo server for tunnel integration tests.
func echoServerForTunnel(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()
	return ln
}
