package auth

import (
	"crypto/tls"
	"fmt"
	"sync/atomic"
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

// Configurator builds TLS configs from file-based certificates and
// supports hot-reload of the leaf certificate via Reload().
type Configurator struct {
	store CertificateStore
	paths TLSPaths

	// currentCert is the live certificate pointer served by the
	// GetCertificate / GetClientCertificate callbacks in the tls.Config
	// returned from ServerTLSConfig / ClientTLSConfig. Swapping this
	// atomically reloads the certificate for the next handshake without
	// rebuilding the tls.Config.
	currentCert atomic.Pointer[tls.Certificate]
}

// Compile-time interface check.
var _ TLSConfigurator = (*Configurator)(nil)

// NewConfigurator creates a Configurator that loads certs via the given store.
//
//nolint:gocritic // TLSPaths is a small config struct, value semantics preferred
func NewConfigurator(store CertificateStore, paths TLSPaths) *Configurator {
	return &Configurator{store: store, paths: paths}
}

// Reload swaps the certificate served by the tls.Config callbacks.
// Safe to call from any goroutine, including concurrent with active
// TLS handshakes. The new certificate takes effect on the next
// handshake; existing connections are not torn down.
func (c *Configurator) Reload(cert tls.Certificate) {
	c.currentCert.Store(&cert)
}

// ServerTLSConfig returns a tls.Config for the relay listener with
// mTLS. The returned config uses a GetCertificate callback that reads
// the current certificate from an atomic pointer, so calling Reload()
// later swaps the certificate for new handshakes without rebuilding
// the config or restarting the listener.
func (c *Configurator) ServerTLSConfig(opts ...TLSOption) (*tls.Config, error) {
	o := tlsOptions{MinVersion: tls.VersionTLS13}
	for _, fn := range opts {
		fn(&o)
	}

	cert, err := c.store.LoadCertificate(c.paths.CertFile, c.paths.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("auth: server tls: %w", err)
	}
	c.currentCert.Store(&cert)

	clientCAs, err := c.store.LoadCertificateAuthority(c.paths.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("auth: server tls: client CA: %w", err)
	}

	return &tls.Config{ //nolint:gosec // default is TLS 1.3; WithMinVersion is for testing only
		MinVersion: o.MinVersion,
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return c.currentCert.Load(), nil
		},
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAs,
	}, nil
}

// ClientTLSConfig returns a tls.Config for the agent dialer with
// mTLS. The returned config uses a GetClientCertificate callback so
// calling Reload() swaps the certificate for the next handshake.
func (c *Configurator) ClientTLSConfig(opts ...TLSOption) (*tls.Config, error) {
	o := tlsOptions{MinVersion: tls.VersionTLS13}
	for _, fn := range opts {
		fn(&o)
	}

	cert, err := c.store.LoadCertificate(c.paths.CertFile, c.paths.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("auth: client tls: %w", err)
	}
	c.currentCert.Store(&cert)

	rootCAs, err := c.store.LoadCertificateAuthority(c.paths.CAFile)
	if err != nil {
		return nil, fmt.Errorf("auth: client tls: root CA: %w", err)
	}

	cfg := &tls.Config{ //nolint:gosec // default is TLS 1.3; WithMinVersion is for testing only
		MinVersion: o.MinVersion,
		GetClientCertificate: func(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return c.currentCert.Load(), nil
		},
		RootCAs:    rootCAs,
		ServerName: c.paths.ServerName,
	}

	if o.SessionCache != nil {
		cfg.ClientSessionCache = o.SessionCache
	}

	return cfg, nil
}
