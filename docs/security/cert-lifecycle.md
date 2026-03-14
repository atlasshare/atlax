# Certificate Lifecycle Management

## Overview

atlax relies on a certificate hierarchy where each level has a distinct validity
period and rotation strategy. Short-lived leaf certificates limit the exposure
window if a key is compromised, while long-lived CA certificates provide
stability at the trust anchor level. Automated rotation is mandatory for leaf
certificates to prevent service disruptions from expired credentials.

## Validity Periods

| Certificate | Validity | Rotation Method | Rotation Trigger |
|-------------|----------|-----------------|------------------|
| Root CA | 10 years | Manual offline ceremony | Planned, well before expiry |
| Relay Intermediate CA | 3 years | Manual, planned rotation | Scheduled or on compromise |
| Customer Intermediate CA | 3 years | Manual, planned rotation | Scheduled or on compromise |
| Relay server cert (leaf) | 90 days | Automated | Less than 30 days remaining |
| Customer agent cert (leaf) | 90 days | Automated | Less than 30 days remaining |

### Rationale for 90-Day Leaf Validity

- The CA/Browser Forum is progressively reducing public TLS certificate
  lifetimes, targeting 47 days by 2029. A 90-day validity for internal mTLS
  certificates follows industry trajectory.
- Short validity limits the window during which a compromised certificate can
  be used. Even without explicit revocation (CRL/OCSP), a stolen 90-day
  certificate becomes useless after expiry.
- Automated rotation removes the operational burden of short validity. The
  system handles renewal transparently without human intervention.

## Automated Rotation Flow

The following sequence describes how an agent rotates its certificate before
expiry:

```
Agent startup / daily check
       |
       v
Read current certificate from disk
       |
       v
Check NotAfter field
       |
       +--- More than 30 days remaining ---> No action, schedule next check
       |
       +--- 30 days or fewer remaining
       |
       v
Generate new key pair (ECDSA P-256)
       |
       v
Create Certificate Signing Request (CSR)
  - Subject: CN=customer-{uuid}, O=AtlasShare
  - Key: the newly generated public key
       |
       v
Submit CSR to AtlasShare control plane API
  - Endpoint: POST /v1/certs/renew
  - Authentication: current (still valid) mTLS certificate
  - Body: PEM-encoded CSR
       |
       v
Control plane validates request
  - Verify the requesting agent's identity matches the CSR subject
  - Sign CSR with Customer Intermediate CA
  - Log issuance in audit trail
       |
       v
Agent receives signed certificate (PEM)
       |
       v
Validate new certificate
  - Verify chain: new cert -> Customer Intermediate CA -> Root CA
  - Verify subject matches expected CN
  - Verify NotBefore <= now <= NotAfter
       |
       v
Write new certificate and key to disk (atomic: temp file + rename)
  - File permissions: 0600 (owner read/write only)
  - Key file: 0600
       |
       v
Hot-reload TLS configuration
  - Update in-memory tls.Certificate
  - New handshakes use the new certificate
  - Existing tunnel connection continues with old session until reconnect
       |
       v
Log successful rotation at INFO level
```

### Overlap Period

The old certificate remains valid until its original expiry date. This provides
a safety window:

- If the new certificate has issues (for example, the control plane signed it
  with incorrect attributes), the agent can fall back to the old certificate
  until the problem is resolved.
- If the agent restarts during the overlap period before the new certificate is
  written to disk, the old certificate is still usable.

Typical timeline:

```
Day 0         Day 60        Day 90
  |             |             |
  +-- cert issued             |
                +-- renewal triggered (30 days before expiry)
                |             |
                +-- new cert issued (valid for 90 days from now)
                |             |
                |             +-- old cert expires
                |
                +-- overlap: both certs valid from day 60 to day 90
```

## Hot-Reload Without Restart

The agent supports certificate hot-reload using Go's `tls.Config` certificate
callback mechanism:

```go
type CertManager struct {
    mu   sync.RWMutex
    cert *tls.Certificate
}

func (cm *CertManager) GetCertificate() (*tls.Certificate, error) {
    cm.mu.RLock()
    defer cm.mu.RUnlock()
    return cm.cert, nil
}

func (cm *CertManager) Reload(certFile, keyFile string) error {
    newCert, err := tls.LoadX509KeyPair(certFile, keyFile)
    if err != nil {
        return fmt.Errorf("load new certificate: %w", err)
    }
    cm.mu.Lock()
    cm.cert = &newCert
    cm.mu.Unlock()
    return nil
}
```

The `tls.Config` references the `CertManager.GetCertificate` method:

```go
tlsConfig := &tls.Config{
    GetClientCertificate: func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
        return certManager.GetCertificate()
    },
    // ... other fields
}
```

When the agent's renewal goroutine writes a new certificate to disk, it calls
`certManager.Reload(...)`. The next TLS handshake (on reconnection or new
connection) uses the new certificate. Existing connections are not disrupted.

## Root CA Rotation

Root CA rotation is a rare, high-impact event. It requires careful planning:

1. **Generate new Root CA** with a new key pair. Set validity to 10 years.
2. **Cross-sign.** The old Root CA signs a certificate for the new Root CA, and
   vice versa. This creates a trust bridge during the transition.
3. **Distribute new Root CA** to all relays and agents. Both the old and new
   Root CAs are trusted during the transition period.
4. **Re-sign Intermediate CAs** under the new Root CA.
5. **Remove old Root CA** from trust stores after all leaf certificates have
   been rotated to chains terminating at the new Root CA.

The full transition takes several months and should be planned well in advance
of the old Root CA's expiry.

## Intermediate CA Rotation

Intermediate CA rotation follows a similar cross-signing approach:

1. Generate a new Intermediate CA key pair.
2. Sign the new Intermediate CA with the Root CA.
3. Distribute the new Intermediate CA to the appropriate trust stores (relay
   for Customer Intermediate CA; agent for Relay Intermediate CA).
4. Begin signing new leaf certificates with the new Intermediate CA.
5. Allow existing leaf certificates (signed by the old Intermediate CA) to
   expire naturally (within 90 days).
6. Remove the old Intermediate CA from trust stores after all old leaf
   certificates have expired.

## Revocation

In addition to short validity periods, atlax supports certificate revocation
for immediate invalidation of compromised certificates:

### Certificate Revocation List (CRL)

- The control plane publishes a CRL signed by the relevant Intermediate CA.
- Relays periodically fetch the CRL (default: every 1 hour) and cache it.
- On each mTLS handshake, the relay checks the agent's certificate against the
  CRL.
- CRL distribution point is embedded in the certificate's CRL Distribution
  Points extension.

### Future: OCSP Stapling

A future enhancement may add OCSP (Online Certificate Status Protocol) support,
where the relay staples an OCSP response to its TLS handshake, and agents
perform OCSP checks on the relay's certificate. This provides real-time
revocation checking without the latency of CRL fetching.

## Configuration

```yaml
cert_rotation:
  check_interval: 24h          # How often to check certificate expiry
  renew_before_expiry: 720h    # 30 days (in hours)
  csr_endpoint: "https://api.atlasshare.io/v1/certs/renew"
  reuse_private_key: false     # Generate fresh key pair each rotation
  cert_file: "/etc/atlax/customer-cert.pem"
  key_file: "/etc/atlax/customer-key.pem"
  ca_file: "/etc/atlax/customer-intermediate-ca.pem"
```
