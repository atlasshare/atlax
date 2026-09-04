package agent

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// closingServer models an HTTP backend that answers one request and then
// closes the socket, the way Node.js closes an idle keep-alive connection.
func closingServer(t *testing.T, reply string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		if _, readErr := bufio.NewReader(conn).ReadString('\n'); readErr != nil {
			return
		}
		conn.Write([]byte(reply)) //nolint:errcheck // test helper
	}()
	return ln
}

// waitForward asserts Forward returns within d and hands back its error.
func waitForward(t *testing.T, done <-chan error, d time.Duration, why string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("Forward did not return within %v: %s", d, why)
		return nil
	}
}

// readEOF asserts that the next Read on s reports end of stream within d.
func readEOF(t *testing.T, s protocol.Stream, d time.Duration, why string) {
	t.Helper()
	res := make(chan error, 1)
	go func() {
		_, err := s.Read(make([]byte, 16))
		res <- err
	}()
	select {
	case err := <-res:
		assert.Error(t, err, why)
	case <-time.After(d):
		t.Fatalf("stream Read still blocked after %v: %s", d, why)
	}
}

func TestNewForwarder_LingerDefault(t *testing.T) {
	fwd := NewForwarder(ServiceForwarderConfig{}, slog.Default())
	assert.Equal(t, defaultLingerTimeout, fwd.config.LingerTimeout)
}

// When the local backend closes its side, the agent must propagate that
// close to the relay so the relay (and any proxy pool in front of it)
// learns the connection is dead instead of reusing it.
func TestForwarder_LocalCloseHalfClosesStream(t *testing.T) {
	ln := closingServer(t, "reply\n")
	fwd := testForwarder()
	relayStream, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fwd.Forward(ctx, agentStream, ln.Addr().String()) }()

	_, err := relayStream.Write([]byte("GET / HTTP/1.1\n"))
	require.NoError(t, err)
	line, err := bufio.NewReader(relayStream).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "reply\n", line)

	// Backend has closed. The relay side must observe EOF without any
	// action of its own.
	readEOF(t, relayStream, 2*time.Second, "agent did not propagate local close")

	// Relay acknowledges by closing its side; Forward must then finish.
	require.NoError(t, relayStream.Close())
	assert.NoError(t, waitForward(t, done, 2*time.Second, "after both sides closed"))
}

// When the relay resets the stream, Forward must return promptly and
// release the local socket, without waiting for the accept context.
func TestForwarder_ReturnsAfterRelayReset(t *testing.T) {
	echo := echoServer(t)
	fwd := testForwarder()
	relayStream, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fwd.Forward(ctx, agentStream, echo.Addr().String()) }()

	_, err := relayStream.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 16)
	n, err := relayStream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "ping", string(buf[:n]))

	rs, ok := relayStream.(*protocol.StreamSession)
	require.True(t, ok)
	rs.Reset(0)

	waitForward(t, done, 2*time.Second, "after relay reset")
}

// If the relay never reacts to the agent's half-close, the agent must
// not wait forever: after LingerTimeout it resets the stream and returns.
func TestForwarder_LingerTimeoutAfterLocalClose(t *testing.T) {
	ln := closingServer(t, "reply\n")
	fwd := NewForwarder(ServiceForwarderConfig{
		DialTimeout:   2 * time.Second,
		BufferSize:    1024,
		LingerTimeout: 200 * time.Millisecond,
	}, slog.Default())
	relayStream, agentStream := muxStreamPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fwd.Forward(ctx, agentStream, ln.Addr().String()) }()

	_, err := relayStream.Write([]byte("GET / HTTP/1.1\n"))
	require.NoError(t, err)
	line, err := bufio.NewReader(relayStream).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "reply\n", line)

	// Relay side deliberately does nothing.
	assert.NoError(t, waitForward(t, done, 1500*time.Millisecond, "linger timeout did not fire"))
}
