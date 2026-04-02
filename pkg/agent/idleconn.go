package agent

import (
	"net"
	"time"
)

// idleConn wraps a net.Conn and resets the read/write deadline on every
// successful operation. If no data flows for the configured timeout,
// the deadline fires and the next Read/Write returns an error.
type idleConn struct {
	conn    net.Conn
	timeout time.Duration
}

// newIdleConn wraps conn with idle timeout. If timeout is 0, returns
// conn unchanged.
func newIdleConn(conn net.Conn, timeout time.Duration) net.Conn {
	if timeout <= 0 {
		return conn
	}
	return &idleConn{conn: conn, timeout: timeout}
}

func (c *idleConn) Read(p []byte) (int, error) {
	if err := c.conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.conn.Read(p)
}

func (c *idleConn) Write(p []byte) (int, error) {
	if err := c.conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.conn.Write(p)
}

func (c *idleConn) Close() error                       { return c.conn.Close() }
func (c *idleConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *idleConn) RemoteAddr() net.Addr               { return c.conn.RemoteAddr() }
func (c *idleConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *idleConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *idleConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
