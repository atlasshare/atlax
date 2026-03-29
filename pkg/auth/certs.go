package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"time"
)

// CertificateStore abstracts loading, caching, and live-rotation of TLS
// certificates and CA bundles.
type CertificateStore interface {
	// LoadCertificate reads a PEM-encoded certificate and key pair from
	// the given cert and key file paths.
	LoadCertificate(certPath, keyPath string) (tls.Certificate, error)

	// LoadCertificateAuthority reads one or more PEM-encoded CA certificates
	// into a pool suitable for peer verification.
	LoadCertificateAuthority(path string) (*x509.CertPool, error)

	// WatchForRotation monitors the given cert and key paths and calls reload
	// whenever the files change on disk. Blocks until ctx is canceled.
	WatchForRotation(ctx context.Context, certPath string, keyPath string, reload func(tls.Certificate)) error
}

// CertRotationConfig controls the automatic certificate rotation watcher.
type CertRotationConfig struct {
	CheckInterval     time.Duration
	RenewBeforeExpiry time.Duration
	CertPath          string
	KeyPath           string
}

// CertInfo describes metadata about a loaded certificate.
type CertInfo struct {
	Subject      string
	Issuer       string
	NotBefore    time.Time
	NotAfter     time.Time
	SerialNumber string
	Fingerprint  string
}
