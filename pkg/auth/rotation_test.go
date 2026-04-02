package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateSelfSignedCert creates a self-signed cert+key pair and writes
// them to the given paths. Returns the cert fingerprint for comparison.
func generateSelfSignedCert(t *testing.T, certPath, keyPath, cn string) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	require.NoError(t, os.WriteFile(certPath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	return CertFingerprint(certDER)
}

func TestCertRotation_FullLifecycle(t *testing.T) {
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "server.crt")
	keyPath := filepath.Join(tmp, "server.key")

	// Generate initial cert
	fp1 := generateSelfSignedCert(t, certPath, keyPath, "initial.test")

	store := NewFileStore(WithCheckInterval(30 * time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reloaded := make(chan tls.Certificate, 1)
	go func() {
		store.WatchForRotation(ctx, certPath, keyPath, func(c tls.Certificate) {
			select {
			case reloaded <- c:
			default:
			}
		}) //nolint:errcheck // ctx cancel expected
	}()

	// Wait for initial fingerprint to be captured
	time.Sleep(100 * time.Millisecond)

	// Generate new cert (different key, different fingerprint)
	fp2 := generateSelfSignedCert(t, certPath, keyPath, "rotated.test")
	assert.NotEqual(t, fp1, fp2, "rotated cert should have different fingerprint")

	// WatchForRotation should detect the change and call reload
	select {
	case cert := <-reloaded:
		assert.NotEmpty(t, cert.Certificate, "reloaded cert should have data")

		// Parse the reloaded cert and verify it's the rotated one
		parsed, err := x509.ParseCertificate(cert.Certificate[0])
		require.NoError(t, err)
		assert.Equal(t, "rotated.test", parsed.Subject.CommonName)
	case <-ctx.Done():
		t.Fatal("WatchForRotation should have called reload after cert change")
	}
}

func TestCertRotation_ReloadedCertHasDifferentFingerprint(t *testing.T) {
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "server.crt")
	keyPath := filepath.Join(tmp, "server.key")

	// Generate initial cert and load it
	generateSelfSignedCert(t, certPath, keyPath, "initial.test")
	cert1, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)
	fp1 := CertFingerprint(cert1.Certificate[0])

	// Generate rotated cert and load it
	generateSelfSignedCert(t, certPath, keyPath, "rotated.test")
	cert2, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)
	fp2 := CertFingerprint(cert2.Certificate[0])

	assert.NotEqual(t, fp1, fp2, "rotated cert must have different fingerprint")

	// Verify the loaded cert has the rotated CN
	parsed, err := x509.ParseCertificate(cert2.Certificate[0])
	require.NoError(t, err)
	assert.Equal(t, "rotated.test", parsed.Subject.CommonName)
}

func TestCertRotation_AtomicValueSwap(t *testing.T) {
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "server.crt")
	keyPath := filepath.Join(tmp, "server.key")

	// Generate and load initial cert
	generateSelfSignedCert(t, certPath, keyPath, "initial.test")
	initialCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)

	// Simulate hot-reload via atomic.Value (same pattern as production)
	var currentCert atomic.Value
	currentCert.Store(&initialCert)

	// Verify initial
	c1 := currentCert.Load().(*tls.Certificate)
	p1, err := x509.ParseCertificate(c1.Certificate[0])
	require.NoError(t, err)
	assert.Equal(t, "initial.test", p1.Subject.CommonName)

	// Rotate
	generateSelfSignedCert(t, certPath, keyPath, "rotated.test")
	rotatedCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)
	currentCert.Store(&rotatedCert)

	// Verify rotated
	c2 := currentCert.Load().(*tls.Certificate)
	p2, err := x509.ParseCertificate(c2.Certificate[0])
	require.NoError(t, err)
	assert.Equal(t, "rotated.test", p2.Subject.CommonName)
}
