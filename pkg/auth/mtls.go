package auth

import (
	"crypto/tls"
	"time"
)

// TLSMode indicates whether TLS is being configured for the relay side or the
// agent side of a connection.
type TLSMode int

const (
	ModeRelay TLSMode = 0
	ModeAgent TLSMode = 1
)

// tlsOptions carries unexported settings applied through functional options.
type tlsOptions struct {
	MinVersion   uint16
	SessionCache tls.ClientSessionCache
}

// TLSOption is a functional option for customizing TLS configuration.
type TLSOption func(*tlsOptions)

// TLSConfigurator builds TLS configurations for both sides of the tunnel.
type TLSConfigurator interface {
	// ServerTLSConfig returns a tls.Config suitable for the relay listener.
	ServerTLSConfig(opts ...TLSOption) (*tls.Config, error)

	// ClientTLSConfig returns a tls.Config suitable for the agent dialer.
	ClientTLSConfig(opts ...TLSOption) (*tls.Config, error)
}

// Identity holds the authenticated identity extracted from a peer certificate
// presented during the mTLS handshake.
type Identity struct {
	CustomerID      string
	RelayID         string
	CertFingerprint string
	NotBefore       time.Time
	NotAfter        time.Time
}

// ExtractIdentity reads the peer certificate from a completed TLS handshake
// and returns the identity fields encoded in the certificate subject.
func ExtractIdentity(conn *tls.Conn) (*Identity, error) {
	// TODO: implement
	return nil, nil
}
