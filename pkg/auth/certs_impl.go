package auth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// DefaultCheckInterval is the default polling interval for cert rotation.
const DefaultCheckInterval = 24 * time.Hour

// FileStore loads certificates and CA bundles from PEM files on disk.
type FileStore struct {
	checkInterval time.Duration
	logger        *slog.Logger
}

// Compile-time interface check.
var _ CertificateStore = (*FileStore)(nil)

// FileStoreOption configures a FileStore.
type FileStoreOption func(*FileStore)

// WithCheckInterval sets the polling interval for WatchForRotation.
func WithCheckInterval(d time.Duration) FileStoreOption {
	return func(s *FileStore) { s.checkInterval = d }
}

// WithLogger sets the logger for the FileStore.
func WithLogger(l *slog.Logger) FileStoreOption {
	return func(s *FileStore) { s.logger = l }
}

// NewFileStore returns a new FileStore.
func NewFileStore(opts ...FileStoreOption) *FileStore {
	s := &FileStore{
		checkInterval: DefaultCheckInterval,
		logger:        slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// LoadCertificate reads a PEM-encoded certificate and key pair.
func (s *FileStore) LoadCertificate(certPath, keyPath string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("auth: load certificate: %w", err)
	}
	return cert, nil
}

// ValidateChainCertFile reads a PEM file and verifies it contains at
// least two certificates (a leaf cert followed by an intermediate CA).
// This is a pre-flight check to catch the common mistake of pointing
// cert_file at a bare leaf cert -- the TLS handshake then fails with
// a cryptic "unknown certificate authority" error on the peer side.
//
// Returns nil on success. Returns an error that explains what's wrong
// and how to fix it if the file contains a single certificate.
func ValidateChainCertFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("auth: validate chain: read %s: %w", path, err)
	}

	count := 0
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			count++
		}
	}

	if count == 0 {
		return fmt.Errorf("auth: validate chain: no certificates found in %s", path)
	}
	if count == 1 {
		return fmt.Errorf(
			"auth: validate chain: %s contains a single certificate (bare leaf, no intermediate CA). "+
				"atlax requires a chain cert: concatenate the leaf and intermediate CA into a single file "+
				"(e.g. `cat leaf.crt intermediate.crt > chain.crt`) and point cert_file at the chain",
			path,
		)
	}
	return nil
}

// LoadCertificateAuthority reads one or more PEM-encoded CA certificates
// and returns an x509.CertPool.
func (s *FileStore) LoadCertificateAuthority(path string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: load ca: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("auth: load ca: no valid certificates in %s", path)
	}
	return pool, nil
}

// WatchForRotation polls the cert and key files and calls reload when
// the certificate changes. Blocks until ctx is canceled.
func (s *FileStore) WatchForRotation(
	ctx context.Context,
	certPath string,
	keyPath string,
	reload func(tls.Certificate),
) error {
	lastFingerprint, err := s.certFingerprint(certPath)
	if err != nil {
		return fmt.Errorf("auth: watch: initial fingerprint: %w", err)
	}

	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			fp, fpErr := s.certFingerprint(certPath)
			if fpErr != nil {
				s.logger.Warn("auth: watch: fingerprint check failed",
					"error", fpErr)
				continue
			}
			if fp == lastFingerprint {
				continue
			}
			cert, loadErr := s.LoadCertificate(certPath, keyPath)
			if loadErr != nil {
				s.logger.Error("auth: watch: reload failed",
					"error", loadErr)
				continue
			}
			lastFingerprint = fp
			reload(cert)
			s.logger.Info("auth: certificate rotated",
				"cert", certPath)
		}
	}
}

// certFingerprint computes SHA-256 of the raw cert file bytes.
func (s *FileStore) certFingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// CertFingerprint computes the SHA-256 fingerprint of DER-encoded cert bytes.
func CertFingerprint(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
