package auth

import (
	"context"
	"crypto/tls"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileStore_LoadCertificate_Valid(t *testing.T) {
	store := NewFileStore()
	cert, err := store.LoadCertificate(
		filepath.Join(testCertsDir(), "relay.crt"),
		filepath.Join(testCertsDir(), "relay.key"),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, cert.Certificate)
}

func TestFileStore_LoadCertificate_MissingCert(t *testing.T) {
	store := NewFileStore()
	_, err := store.LoadCertificate(
		"/nonexistent/cert.pem",
		filepath.Join(testCertsDir(), "relay.key"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth: load certificate")
}

func TestFileStore_LoadCertificate_MissingKey(t *testing.T) {
	store := NewFileStore()
	_, err := store.LoadCertificate(
		filepath.Join(testCertsDir(), "relay.crt"),
		"/nonexistent/key.pem",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth: load certificate")
}

func TestFileStore_LoadCertificate_Mismatch(t *testing.T) {
	// Use relay cert with agent key -- should fail
	store := NewFileStore()
	_, err := store.LoadCertificate(
		filepath.Join(testCertsDir(), "relay.crt"),
		filepath.Join(testCertsDir(), "agent.key"),
	)
	require.Error(t, err)
}

func TestFileStore_LoadCA_Valid(t *testing.T) {
	store := NewFileStore()
	pool, err := store.LoadCertificateAuthority(
		filepath.Join(testCertsDir(), "root-ca.crt"),
	)
	require.NoError(t, err)
	assert.NotNil(t, pool)
}

func TestFileStore_LoadCA_MissingFile(t *testing.T) {
	store := NewFileStore()
	_, err := store.LoadCertificateAuthority("/nonexistent/ca.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth: load ca")
}

func TestFileStore_LoadCA_InvalidPEM(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.pem")
	require.NoError(t, os.WriteFile(bad, []byte("not a cert"), 0o600))

	store := NewFileStore()
	_, err := store.LoadCertificateAuthority(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid certificates")
}

func TestFileStore_WatchForRotation_CallsReload(t *testing.T) {
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "cert.crt")
	keyPath := filepath.Join(tmp, "cert.key")

	// Copy real certs to temp dir
	srcCert := filepath.Join(testCertsDir(), "relay.crt")
	srcKey := filepath.Join(testCertsDir(), "relay.key")
	copyFile(t, srcCert, certPath)
	copyFile(t, srcKey, keyPath)

	store := NewFileStore(WithCheckInterval(50 * time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reloaded := make(chan tls.Certificate, 1)
	go func() {
		//nolint:errcheck // ctx cancellation is expected
		store.WatchForRotation(ctx, certPath, keyPath, func(c tls.Certificate) {
			select {
			case reloaded <- c:
			default:
			}
		})
	}()

	// Wait for initial fingerprint to be set
	time.Sleep(100 * time.Millisecond)

	// Replace cert with agent cert (different content, same key format trick won't work)
	// Instead, just rewrite the same cert to trigger a no-change, then write different bytes
	// to simulate rotation. We append a newline to change the file hash.
	data, err := os.ReadFile(certPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certPath, append(data, '\n'), 0o600))

	select {
	case c := <-reloaded:
		assert.NotEmpty(t, c.Certificate)
	case <-ctx.Done():
		t.Fatal("WatchForRotation should have called reload after file change")
	}
}

func TestFileStore_WatchForRotation_RespectsContext(t *testing.T) {
	store := NewFileStore(WithCheckInterval(10 * time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())

	certPath := filepath.Join(testCertsDir(), "relay.crt")
	keyPath := filepath.Join(testCertsDir(), "relay.key")

	done := make(chan error, 1)
	go func() {
		done <- store.WatchForRotation(ctx, certPath, keyPath, func(_ tls.Certificate) {})
	}()

	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("WatchForRotation should return on context cancel")
	}
}

func TestFileStore_WithLogger(t *testing.T) {
	logger := slog.Default()
	store := NewFileStore(WithLogger(logger))
	assert.NotNil(t, store)
}

func TestCertFingerprint(t *testing.T) {
	data := []byte("test cert data")
	fp := CertFingerprint(data)
	assert.Len(t, fp, 64) // SHA-256 hex = 64 chars
	// Same input produces same fingerprint
	assert.Equal(t, fp, CertFingerprint(data))
	// Different input produces different fingerprint
	assert.NotEqual(t, fp, CertFingerprint([]byte("other")))
}

func TestFileStore_WatchForRotation_InitialFingerprintError(t *testing.T) {
	store := NewFileStore(WithCheckInterval(10 * time.Millisecond))
	ctx := context.Background()
	err := store.WatchForRotation(ctx, "/nonexistent/cert.pem", "/nonexistent/key.pem",
		func(_ tls.Certificate) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initial fingerprint")
}

// testCertsDir returns the path to the project's dev certificates.
func testCertsDir() string {
	// Tests run from pkg/auth/, certs are at project root
	return filepath.Join("..", "..", "certs")
}

func TestValidateChainCertFile_Chain(t *testing.T) {
	// relay-chain.crt is a proper chain (leaf + intermediate CA).
	err := ValidateChainCertFile(filepath.Join(testCertsDir(), "relay-chain.crt"))
	require.NoError(t, err)
}

func TestValidateChainCertFile_BareCert(t *testing.T) {
	// relay.crt is a bare leaf certificate.
	err := ValidateChainCertFile(filepath.Join(testCertsDir(), "relay.crt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "single certificate")
	assert.Contains(t, err.Error(), "bare leaf")
	assert.Contains(t, err.Error(), "intermediate")
}

func TestValidateChainCertFile_Missing(t *testing.T) {
	err := ValidateChainCertFile("/nonexistent/chain.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read")
}

func TestValidateChainCertFile_Empty(t *testing.T) {
	tmp := t.TempDir()
	empty := filepath.Join(tmp, "empty.pem")
	require.NoError(t, os.WriteFile(empty, []byte("not a cert"), 0o600))

	err := ValidateChainCertFile(empty)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no certificates found")
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o600))
}
