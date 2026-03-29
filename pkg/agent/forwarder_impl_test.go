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

func TestNewForwarder_Defaults(t *testing.T) {
	fwd := NewForwarder(ServiceForwarderConfig{}, slog.Default())
	assert.Equal(t, defaultBufferSize, fwd.config.BufferSize)
	assert.Equal(t, 5*time.Second, fwd.config.DialTimeout)
}

func testForwarder() *Forwarder {
	return NewForwarder(ServiceForwarderConfig{
		DialTimeout: 2 * time.Second,
		BufferSize:  1024,
	}, slog.Default())
}

// echoServer starts a TCP server that echoes received data back.
func echoServer(t *testing.T) net.Listener {
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

// muxStreamPair creates two connected MuxSessions and returns an open
// stream from the relay side and the accepted stream on the agent side.
func muxStreamPair(t *testing.T) (relayStream, agentStream protocol.Stream) {
	t.Helper()
	c1, c2 := net.Pipe()
	cfg := protocol.MuxConfig{
		MaxConcurrentStreams: 16,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	}
	relay := protocol.NewMuxSession(c1, protocol.RoleRelay, cfg)
	agent := protocol.NewMuxSession(c2, protocol.RoleAgent, cfg)
	t.Cleanup(func() {
		relay.Close()
		agent.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	rs, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	as, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	return rs, as
}

func TestForwarder_StreamToLocal(t *testing.T) {
	echo := echoServer(t)
	fwd := testForwarder()

	relayStream, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Forward agent stream to echo server
	done := make(chan error, 1)
	go func() {
		done <- fwd.Forward(ctx, agentStream, echo.Addr().String())
	}()

	// Write from relay side, echo server reflects it back
	msg := []byte("hello echo")
	_, err := relayStream.Write(msg)
	require.NoError(t, err)

	buf := make([]byte, 64)
	n, err := relayStream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, string(msg), string(buf[:n]))

	// Cancel context to end forwarding cleanly
	cancel()

	select {
	case <-done:
		// completed
	case <-time.After(3 * time.Second):
		t.Fatal("Forward should have completed after context cancel")
	}
}

func TestForwarder_LocalToStream(t *testing.T) {
	// Use a server that writes data then closes
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	msg := []byte("data from local service")
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		conn.Write(msg)
		conn.Close()
	}()

	fwd := testForwarder()
	relayStream, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- fwd.Forward(ctx, agentStream, ln.Addr().String())
	}()

	buf := make([]byte, 64)
	n, readErr := relayStream.Read(buf)
	require.NoError(t, readErr)
	assert.Equal(t, string(msg), string(buf[:n]))

	cancel() // terminate forwarding
	<-done
}

func TestForwarder_Bidirectional(t *testing.T) {
	echo := echoServer(t)
	fwd := testForwarder()
	relayStream, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		fwd.Forward(ctx, agentStream, echo.Addr().String())
	}()

	// Send multiple messages, verify echo
	for _, msg := range []string{"one", "two", "three"} {
		_, err := relayStream.Write([]byte(msg))
		require.NoError(t, err)

		buf := make([]byte, 64)
		n, readErr := relayStream.Read(buf)
		require.NoError(t, readErr)
		assert.Equal(t, msg, string(buf[:n]))
	}
}

func TestForwarder_DialTimeout(t *testing.T) {
	fwd := NewForwarder(ServiceForwarderConfig{
		DialTimeout: 50 * time.Millisecond,
		BufferSize:  1024,
	}, slog.Default())

	_, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Dial a port that nothing listens on
	err := fwd.Forward(ctx, agentStream, "127.0.0.1:1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forwarder: dial")
}

func TestForwarder_ContextCancellation(t *testing.T) {
	echo := echoServer(t)
	fwd := testForwarder()
	_, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- fwd.Forward(ctx, agentStream, echo.Addr().String())
	}()

	// Cancel immediately
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// Should complete without error (context canceled is not an error)
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Forward should return after context cancel")
	}
}

func TestForwarder_LargePayload(t *testing.T) {
	echo := echoServer(t)
	fwd := NewForwarder(ServiceForwarderConfig{
		DialTimeout: 2 * time.Second,
		BufferSize:  256, // small buffer forces multiple reads
	}, slog.Default())

	relayStream, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		fwd.Forward(ctx, agentStream, echo.Addr().String())
	}()

	// Send 8KB (larger than the 256-byte buffer)
	payload := make([]byte, 8192)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	_, err := relayStream.Write(payload)
	require.NoError(t, err)

	// Read it all back
	result := make([]byte, 0, len(payload))
	buf := make([]byte, 1024)
	for len(result) < len(payload) {
		n, readErr := relayStream.Read(buf)
		if readErr != nil {
			break
		}
		result = append(result, buf[:n]...)
	}
	assert.Equal(t, payload, result)
}
