// Package testcerts generates the development certificate hierarchy that
// scripts/gen-certs.sh produces, at test time, into a temporary
// directory. Tests no longer depend on the untracked certs/ directory,
// whose 90-day leaf certificates rot and break the suites.
//
// Layout matches gen-certs.sh: root-ca -> relay-ca -> relay
// (CN=relay.atlax.local, serverAuth, SANs), root-ca -> customer-ca ->
// agent (CN=customer-dev-001, clientAuth), plus relay-chain.crt,
// agent-chain.crt and intermediate-cas.crt bundles.
//
// Set ATLAX_TEST_CERTS_DIR to point tests at an existing directory
// instead, for example freshly generated dev certs.
package testcerts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EnvDir names the environment variable that overrides generation.
const EnvDir = "ATLAX_TEST_CERTS_DIR"

const (
	// Validity is how long generated leaf certificates stay valid. Long
	// enough that a test binary never straddles expiry.
	Validity   = 365 * 24 * time.Hour
	caValidity = 10 * 365 * 24 * time.Hour
)

var (
	once sync.Once
	dir  string
	err  error
)

// Dir returns a directory holding the full certificate hierarchy,
// generating it on first use. Generation happens once per process; call
// Cleanup from TestMain to remove it.
func Dir() (string, error) {
	once.Do(func() {
		if d := os.Getenv(EnvDir); d != "" {
			dir = d
			return
		}
		dir, err = generate()
	})
	return dir, err
}

// MustDir is Dir for test helpers that cannot return an error.
func MustDir() string {
	d, genErr := Dir()
	if genErr != nil {
		panic(fmt.Sprintf("testcerts: %v", genErr))
	}
	return d
}

// Cleanup removes a generated directory. It is a no-op for an
// ATLAX_TEST_CERTS_DIR override.
func Cleanup() {
	if dir != "" && os.Getenv(EnvDir) == "" {
		os.RemoveAll(dir) //nolint:errcheck // best-effort cleanup
	}
}

type keyPair struct {
	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey
}

func generate() (string, error) {
	out, mkErr := os.MkdirTemp("", "atlax-testcerts-")
	if mkErr != nil {
		return "", fmt.Errorf("testcerts: mkdir: %w", mkErr)
	}
	now := time.Now()

	root, genErr := newCA("atlax-root-ca", nil, now, -1)
	if genErr != nil {
		return "", genErr
	}
	relayCA, genErr := newCA("atlax-relay-ca", root, now, 0)
	if genErr != nil {
		return "", genErr
	}
	customerCA, genErr := newCA("atlax-customer-ca", root, now, 0)
	if genErr != nil {
		return "", genErr
	}
	relay, genErr := newLeaf(relayCA, now, &x509.Certificate{
		Subject:     subject("relay.atlax.local"),
		DNSNames:    []string{"relay.atlax.local", "localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if genErr != nil {
		return "", genErr
	}
	agent, genErr := newLeaf(customerCA, now, &x509.Certificate{
		Subject:     subject("customer-dev-001"),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if genErr != nil {
		return "", genErr
	}

	files := map[string][]byte{
		"root-ca.crt":          certPEM(root.der),
		"root-ca.key":          keyPEM(root.key),
		"relay-ca.crt":         certPEM(relayCA.der),
		"relay-ca.key":         keyPEM(relayCA.key),
		"customer-ca.crt":      certPEM(customerCA.der),
		"customer-ca.key":      keyPEM(customerCA.key),
		"relay.crt":            certPEM(relay.der),
		"relay.key":            keyPEM(relay.key),
		"agent.crt":            certPEM(agent.der),
		"agent.key":            keyPEM(agent.key),
		"relay-chain.crt":      append(certPEM(relay.der), certPEM(relayCA.der)...),
		"agent-chain.crt":      append(certPEM(agent.der), certPEM(customerCA.der)...),
		"intermediate-cas.crt": append(certPEM(relayCA.der), certPEM(customerCA.der)...),
	}
	for name, data := range files {
		mode := os.FileMode(0o644)
		if filepath.Ext(name) == ".key" {
			mode = 0o600
		}
		if wErr := os.WriteFile(filepath.Join(out, name), data, mode); wErr != nil {
			return "", fmt.Errorf("testcerts: write %s: %w", name, wErr)
		}
	}
	return out, nil
}

func subject(cn string) pkix.Name {
	return pkix.Name{Country: []string{"US"}, Organization: []string{"AtlasShare"}, CommonName: cn}
}

// newCA issues a CA certificate; parent nil means self-signed root.
// pathLen < 0 means unconstrained.
func newCA(cn string, parent *keyPair, now time.Time, pathLen int) (*keyPair, error) {
	tmpl := &x509.Certificate{
		Subject:               subject(cn),
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	if pathLen >= 0 {
		tmpl.MaxPathLen = pathLen
		tmpl.MaxPathLenZero = pathLen == 0
	}
	return sign(tmpl, parent)
}

func newLeaf(parent *keyPair, now time.Time, tmpl *x509.Certificate) (*keyPair, error) {
	tmpl.NotBefore = now.Add(-time.Hour)
	tmpl.NotAfter = now.Add(Validity)
	tmpl.BasicConstraintsValid = true
	return sign(tmpl, parent)
}

func sign(tmpl *x509.Certificate, parent *keyPair) (*keyPair, error) {
	key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if keyErr != nil {
		return nil, fmt.Errorf("testcerts: key: %w", keyErr)
	}
	serial, serialErr := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if serialErr != nil {
		return nil, fmt.Errorf("testcerts: serial: %w", serialErr)
	}
	tmpl.SerialNumber = serial

	issuer, issuerKey := tmpl, key
	if parent != nil {
		issuer, issuerKey = parent.cert, parent.key
	}
	der, certErr := x509.CreateCertificate(rand.Reader, tmpl, issuer, &key.PublicKey, issuerKey)
	if certErr != nil {
		return nil, fmt.Errorf("testcerts: create %s: %w", tmpl.Subject.CommonName, certErr)
	}
	cert, parseErr := x509.ParseCertificate(der)
	if parseErr != nil {
		return nil, fmt.Errorf("testcerts: parse: %w", parseErr)
	}
	return &keyPair{cert: cert, der: der, key: key}, nil
}

func certPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func keyPEM(key *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(fmt.Sprintf("testcerts: marshal key: %v", err))
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}
