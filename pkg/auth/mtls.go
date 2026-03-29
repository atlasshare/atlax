package auth

import (
	"crypto/tls"
	"fmt"
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

// WithSessionCache enables an LRU session cache of the given size.
func WithSessionCache(size int) TLSOption {
	return func(o *tlsOptions) {
		o.SessionCache = tls.NewLRUClientSessionCache(size)
	}
}

// WithMinVersion overrides the minimum TLS version (testing only).
func WithMinVersion(version uint16) TLSOption {
	return func(o *tlsOptions) {
		o.MinVersion = version
	}
}

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

// TLSPaths holds the file paths needed for mTLS configuration.
type TLSPaths struct {
	CertFile     string
	KeyFile      string
	CAFile       string
	ClientCAFile string
	ServerName   string
}

// Configurator builds TLS configs from file-based certificates.
type Configurator struct {
	store CertificateStore
	paths TLSPaths
}

// Compile-time interface check.
var _ TLSConfigurator = (*Configurator)(nil)

// NewConfigurator creates a Configurator that loads certs via the given store.
//
//nolint:gocritic // TLSPaths is a small config struct, value semantics preferred
func NewConfigurator(store CertificateStore, paths TLSPaths) *Configurator {
	return &Configurator{store: store, paths: paths}
}

// ServerTLSConfig returns a tls.Config for the relay listener with mTLS.
func (c *Configurator) ServerTLSConfig(opts ...TLSOption) (*tls.Config, error) {
	o := tlsOptions{MinVersion: tls.VersionTLS13}
	for _, fn := range opts {
		fn(&o)
	}

	cert, err := c.store.LoadCertificate(c.paths.CertFile, c.paths.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("auth: server tls: %w", err)
	}

	clientCAs, err := c.store.LoadCertificateAuthority(c.paths.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("auth: server tls: client CA: %w", err)
	}

	return &tls.Config{ //nolint:gosec // default is TLS 1.3; WithMinVersion is for testing only
		MinVersion:   o.MinVersion,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}, nil
}

// ClientTLSConfig returns a tls.Config for the agent dialer with mTLS.
func (c *Configurator) ClientTLSConfig(opts ...TLSOption) (*tls.Config, error) {
	o := tlsOptions{MinVersion: tls.VersionTLS13}
	for _, fn := range opts {
		fn(&o)
	}

	cert, err := c.store.LoadCertificate(c.paths.CertFile, c.paths.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("auth: client tls: %w", err)
	}

	rootCAs, err := c.store.LoadCertificateAuthority(c.paths.CAFile)
	if err != nil {
		return nil, fmt.Errorf("auth: client tls: root CA: %w", err)
	}

	cfg := &tls.Config{ //nolint:gosec // default is TLS 1.3; WithMinVersion is for testing only
		MinVersion:   o.MinVersion,
		Certificates: []tls.Certificate{cert},
		RootCAs:      rootCAs,
		ServerName:   c.paths.ServerName,
	}

	if o.SessionCache != nil {
		cfg.ClientSessionCache = o.SessionCache
	}

	return cfg, nil
}
