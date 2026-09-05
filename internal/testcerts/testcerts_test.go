package testcerts

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	code := m.Run()
	Cleanup()
	os.Exit(code)
}

// The generated hierarchy must match what scripts/gen-certs.sh produces:
// the same file names, the CNs the identity code keys on, chain files
// that verify against the root, and validity far beyond a test run.
func TestDir_MatchesDevCertLayout(t *testing.T) {
	if os.Getenv(EnvDir) != "" {
		t.Skip("layout assertions apply to generated certificates only")
	}
	d, err := Dir()
	require.NoError(t, err)
	for _, f := range []string{"root-ca.crt", "relay-ca.crt", "customer-ca.crt", "relay.crt", "relay.key",
		"agent.crt", "agent.key", "relay-chain.crt", "agent-chain.crt", "intermediate-cas.crt"} {
		_, statErr := os.Stat(filepath.Join(d, f))
		assert.NoError(t, statErr, f)
	}

	relay, err := tls.LoadX509KeyPair(filepath.Join(d, "relay.crt"), filepath.Join(d, "relay.key"))
	require.NoError(t, err)
	agent, err := tls.LoadX509KeyPair(filepath.Join(d, "agent.crt"), filepath.Join(d, "agent.key"))
	require.NoError(t, err)
	relayLeaf, _ := x509.ParseCertificate(relay.Certificate[0])
	agentLeaf, _ := x509.ParseCertificate(agent.Certificate[0])
	assert.Equal(t, "relay.atlax.local", relayLeaf.Subject.CommonName)
	assert.Equal(t, "customer-dev-001", agentLeaf.Subject.CommonName)
	assert.Contains(t, relayLeaf.DNSNames, "localhost")
	assert.True(t, relayLeaf.NotAfter.After(time.Now().Add(300*24*time.Hour)))

	roots := x509.NewCertPool()
	rootPEM, _ := os.ReadFile(filepath.Join(d, "root-ca.crt"))
	require.True(t, roots.AppendCertsFromPEM(rootPEM))
	inters := x509.NewCertPool()
	interPEM, _ := os.ReadFile(filepath.Join(d, "intermediate-cas.crt"))
	require.True(t, inters.AppendCertsFromPEM(interPEM))
	_, err = relayLeaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inters, DNSName: "relay.atlax.local"})
	assert.NoError(t, err, "relay leaf chains to root through relay-ca")
	_, err = agentLeaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inters, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	assert.NoError(t, err, "agent leaf chains to root through customer-ca")

	// Same directory on repeated calls within one process.
	d2, err := Dir()
	require.NoError(t, err)
	assert.Equal(t, d, d2)
}
