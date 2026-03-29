package auth

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"strings"
)

// ExtractIdentity reads the peer certificate from a completed TLS handshake
// and returns the identity fields encoded in the certificate subject.
func ExtractIdentity(conn *tls.Conn) (*Identity, error) {
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("auth: extract identity: no peer certificates")
	}

	cert := state.PeerCertificates[0]
	cn := cert.Subject.CommonName

	fp := sha256.Sum256(cert.Raw)

	id := &Identity{
		CertFingerprint: hex.EncodeToString(fp[:]),
		NotBefore:       cert.NotBefore,
		NotAfter:        cert.NotAfter,
	}

	switch {
	case strings.HasPrefix(cn, "customer-"):
		id.CustomerID = cn
	case strings.HasPrefix(cn, "relay"):
		id.RelayID = cn
	default:
		return nil, fmt.Errorf(
			"auth: extract identity: unexpected CN format: %q", cn)
	}

	return id, nil
}
