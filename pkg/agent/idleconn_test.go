package agent

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdleConn_TimeoutOnIdle(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()

	ic := newIdleConn(c1, 50*time.Millisecond)

	// No data written to c2, so Read should timeout
	buf := make([]byte, 10)
	_, err := ic.Read(buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestIdleConn_ActiveConnectionStaysOpen(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	ic := newIdleConn(c1, 200*time.Millisecond)

	// Write data from c2 before timeout
	go func() {
		time.Sleep(50 * time.Millisecond)
		c2.Write([]byte("hello")) //nolint:errcheck // test
	}()

	buf := make([]byte, 10)
	n, err := ic.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(buf[:n]))
}

func TestIdleConn_WriteResetsDeadline(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()

	ic := newIdleConn(c1, 200*time.Millisecond)

	// Read from c2 in background to unblock Write
	go func() {
		buf := make([]byte, 64)
		c2.Read(buf) //nolint:errcheck // test
	}()

	_, err := ic.Write([]byte("data"))
	require.NoError(t, err)
}

func TestIdleConn_ZeroTimeoutPassthrough(t *testing.T) {
	c1, _ := net.Pipe()
	defer c1.Close()

	// Zero timeout should return the original conn, not wrapped
	result := newIdleConn(c1, 0)
	assert.Equal(t, c1, result, "zero timeout should return original conn")
}
