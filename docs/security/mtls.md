# Mutual TLS Authentication

## Overview

atlax uses mutual TLS (mTLS) as the sole authentication mechanism between the
relay and the agent. Both sides present certificates during the TLS handshake,
and both sides verify the peer's certificate chain. There is no fallback to
plaintext, no password-based authentication, and no API key mechanism.

mTLS provides:

- **Mutual identity verification.** The relay knows it is talking to a
  legitimate customer agent. The agent knows it is talking to the legitimate
  relay.
- **Cryptographic tenant identity.** The customer UUID is embedded in the
  certificate's Common Name, eliminating the need for a separate identity
  token.
- **Transport encryption.** All data is encrypted with TLS 1.3 cipher suites.
- **Replay protection.** TLS 1.3 handshake prevents replay attacks.

## Certificate Hierarchy

```
AtlasShare Root CA
(self-signed, offline, 10-year validity)
       |
       +--- Relay Intermediate CA
       |    (3-year validity, signs relay server certs)
       |         |
       |         +--- relay.atlasshare.io
       |              (server cert, 90-day validity)
       |
       +--- Customer Intermediate CA
            (3-year validity, signs customer agent certs)
                 |
                 +--- customer-{uuid}.atlasshare.io
                      (client cert, 90-day validity)
```

Key properties of this hierarchy:

- **Root CA is offline.** The Root CA private key is stored offline in a secure
  location (HSM or air-gapped machine). It is only used to sign Intermediate
  CA certificates, which happens rarely (every 3 years or on revocation).
- **Intermediate CAs are scoped.** The Relay Intermediate CA signs only relay
  server certificates. The Customer Intermediate CA signs only customer agent
  certificates. This limits the blast radius if an intermediate key is
  compromised.
- **Leaf certificates are short-lived.** 90-day validity with automated
  rotation ensures that a compromised leaf certificate has a bounded exposure
  window.

## TLS 1.3 Requirement

atlax requires TLS 1.3 as the minimum protocol version. TLS 1.2 and below are
explicitly disabled.

Rationale:

- **Simplified handshake.** TLS 1.3 completes in 1-RTT (or 0-RTT with session
  resumption), reducing connection establishment latency.
- **Stronger cipher suites.** TLS 1.3 removes support for weak algorithms
  (RSA key exchange, CBC mode, SHA-1, RC4, DES, 3DES). All cipher suites use
  AEAD encryption and ephemeral key exchange (ECDHE or DHE).
- **Forward secrecy by default.** Every TLS 1.3 cipher suite provides forward
  secrecy, meaning that compromise of a long-term private key does not allow
  decryption of past sessions.
- **No downgrade attacks.** TLS 1.3 includes anti-downgrade mechanisms in the
  handshake that prevent an attacker from forcing a fallback to an older
  protocol version.

## Relay TLS Configuration

The relay's TLS listener (for agent connections) is configured as follows:

```go
relayCert, err := tls.LoadX509KeyPair("relay-cert.pem", "relay-key.pem")
if err != nil {
    log.Fatal("failed to load relay certificate:", err)
}

customerCACertPool := x509.NewCertPool()
customerCAPEM, err := os.ReadFile("customer-intermediate-ca.pem")
if err != nil {
    log.Fatal("failed to load customer CA:", err)
}
customerCACertPool.AppendCertsFromPEM(customerCAPEM)

tlsConfig := &tls.Config{
    MinVersion:   tls.VersionTLS13,
    ClientAuth:   tls.RequireAndVerifyClientCert,
    ClientCAs:    customerCACertPool,
    Certificates: []tls.Certificate{relayCert},

    // Session resumption for reconnection performance
    SessionTicketsDisabled: false,
}
```

Key configuration points:

- `MinVersion: tls.VersionTLS13` -- Rejects any connection attempting TLS 1.2
  or below.
- `ClientAuth: tls.RequireAndVerifyClientCert` -- The relay requires the agent
  to present a certificate and verifies it against the Customer Intermediate CA
  pool. Connections without a valid client certificate are refused.
- `ClientCAs` -- Contains only the Customer Intermediate CA certificate. The
  relay does not trust the Root CA directly for client authentication; it trusts
  the specific intermediate that signs customer certificates.
- `SessionTicketsDisabled: false` -- Enables TLS session tickets for fast
  reconnection (1-RTT or 0-RTT handshake on subsequent connections).

## Agent TLS Configuration

The agent's TLS client (for connecting to the relay) is configured as follows:

```go
customerCert, err := tls.LoadX509KeyPair("customer-cert.pem", "customer-key.pem")
if err != nil {
    log.Fatal("failed to load customer certificate:", err)
}

relayCACertPool := x509.NewCertPool()
relayCAPEM, err := os.ReadFile("relay-intermediate-ca.pem")
if err != nil {
    log.Fatal("failed to load relay CA:", err)
}
relayCACertPool.AppendCertsFromPEM(relayCAPEM)

tlsConfig := &tls.Config{
    MinVersion:   tls.VersionTLS13,
    Certificates: []tls.Certificate{customerCert},
    RootCAs:      relayCACertPool,
    ServerName:   "relay.atlasshare.io",
}
```

Key configuration points:

- `Certificates` -- The agent presents its customer certificate during the
  handshake.
- `RootCAs` -- Contains the Relay Intermediate CA certificate. The agent
  verifies that the relay's server certificate is signed by the expected
  intermediate CA.
- `ServerName` -- The expected Subject Alternative Name (SAN) or Common Name
  on the relay's certificate. This prevents man-in-the-middle attacks where an
  attacker presents a valid certificate for a different domain.

## Identity Extraction

After a successful mTLS handshake on the relay side, the customer identity is
extracted from the peer certificate's Common Name:

```go
func extractCustomerID(conn *tls.Conn) (string, error) {
    state := conn.ConnectionState()
    if len(state.PeerCertificates) == 0 {
        return "", fmt.Errorf("no peer certificates presented")
    }

    cn := state.PeerCertificates[0].Subject.CommonName
    // Expected format: customer-{uuid}
    if !strings.HasPrefix(cn, "customer-") {
        return "", fmt.Errorf("unexpected CN format: %s", cn)
    }

    return cn, nil
}
```

The extracted customer ID (e.g., `customer-a1b2c3d4-e5f6-7890-abcd-ef1234567890`)
is used as the tenant identifier throughout the relay: in the Agent Registry,
in metrics labels, in access logs, and in port-to-customer mappings.

### Certificate Subject Format

| Field | Value | Example |
|-------|-------|---------|
| Common Name (CN) | `customer-{uuid}` | `customer-a1b2c3d4-e5f6-7890-abcd-ef1234567890` |
| Organization (O) | `AtlasShare` | `AtlasShare` |
| Organizational Unit (OU) | Customer tier (optional) | `community` or `enterprise` |

## Common Mistakes to Avoid

### 1. Trusting the Root CA directly for client authentication

Do not add the Root CA to the relay's `ClientCAs` pool. If you trust the Root
CA, any certificate signed by any intermediate under the root would be accepted,
including relay server certificates used as client certificates. Trust only the
specific Customer Intermediate CA.

### 2. Disabling certificate verification for testing

Never set `InsecureSkipVerify: true` in production or development environments
that handle real traffic. If you need to test without a full CA hierarchy, use
a self-signed CA and configure it properly.

### 3. Using TLS 1.2 cipher suites

Go's `crypto/tls` package correctly refuses TLS 1.2 cipher suites when
`MinVersion` is set to TLS 1.3. Do not attempt to configure `CipherSuites`
when using TLS 1.3; the field is ignored and the implementation selects the
strongest available AEAD cipher.

### 4. Hardcoding certificate paths

Certificate file paths should come from configuration (YAML file or environment
variables), not from hardcoded strings. This enables rotation, deployment
flexibility, and testing with different certificates.

### 5. Ignoring session resumption

Session resumption significantly reduces handshake latency for reconnecting
agents. Ensure `SessionTicketsDisabled` is `false` (the default) on the relay.
On the agent side, use a `SessionCache` to store session tickets:

```go
tlsConfig.ClientSessionCache = tls.NewLRUClientSessionCache(64)
```

### 6. Not validating the peer certificate chain programmatically

Go's TLS library performs chain validation automatically when `ClientAuth` is
set to `RequireAndVerifyClientCert` and `ClientCAs` is populated. Do not
re-implement chain validation manually. However, you should still verify the
Common Name format and extract the customer ID as shown above.

### 7. Reusing private keys across certificate rotations

While reusing the private key simplifies rotation (only the certificate
changes, not the key pair), it means that compromise of the key at any point
exposes all future certificates until the key is finally changed. The default
recommendation is to generate a fresh key pair on each rotation.
