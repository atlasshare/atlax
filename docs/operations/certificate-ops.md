# Certificate Operations

This document covers the full certificate lifecycle for atlax: development CA setup, production CA options, issuance workflows, revocation, emergency rotation, and monitoring.

---

## Certificate Hierarchy

atlax uses a two-tier CA hierarchy with separate intermediate CAs for relay and customer certificates:

```
AtlasShare Root CA (offline, 10-year validity)
       |
       +-- Relay Intermediate CA (3-year validity)
       |         |
       |         +-- relay.atlasshare.io (server cert, 90-day validity)
       |
       +-- Customer Intermediate CA (3-year validity)
                 |
                 +-- customer-{uuid}.atlasshare.io (client cert, 90-day validity)
```

### Validity Periods

| Certificate | Validity | Rotation |
|-------------|----------|----------|
| Root CA | 10 years | Manual offline ceremony |
| Relay Intermediate CA | 3 years | Planned manual rotation |
| Customer Intermediate CA | 3 years | Planned manual rotation |
| Relay server certificate | 90 days | Automated |
| Customer agent certificate | 90 days | Automated |

---

## Development CA Setup (OpenSSL)

For local development and testing, use self-signed certificates generated with OpenSSL. The `scripts/gen-certs.sh` script automates this process.

### Manual Steps

**1. Create the Root CA:**

```bash
# Generate root CA private key
openssl ecparam -genkey -name prime256v1 -out root-ca.key

# Create self-signed root CA certificate (10 years)
openssl req -new -x509 -sha256 -key root-ca.key \
  -out root-ca.crt -days 3650 \
  -subj "/C=US/O=AtlasShare/CN=AtlasShare Root CA"
```

**2. Create the Relay Intermediate CA:**

```bash
# Generate intermediate CA key
openssl ecparam -genkey -name prime256v1 -out relay-intermediate-ca.key

# Create CSR
openssl req -new -sha256 -key relay-intermediate-ca.key \
  -out relay-intermediate-ca.csr \
  -subj "/C=US/O=AtlasShare/CN=AtlasShare Relay CA"

# Sign with Root CA (3 years)
openssl x509 -req -sha256 -in relay-intermediate-ca.csr \
  -CA root-ca.crt -CAkey root-ca.key -CAcreateserial \
  -out relay-intermediate-ca.crt -days 1095 \
  -extfile <(cat <<CONF
basicConstraints = critical, CA:TRUE, pathlen:0
keyUsage = critical, keyCertSign, cRLSign
CONF
)
```

**3. Create the Customer Intermediate CA:**

```bash
# Generate intermediate CA key
openssl ecparam -genkey -name prime256v1 -out customer-intermediate-ca.key

# Create CSR
openssl req -new -sha256 -key customer-intermediate-ca.key \
  -out customer-intermediate-ca.csr \
  -subj "/C=US/O=AtlasShare/CN=AtlasShare Customer CA"

# Sign with Root CA (3 years)
openssl x509 -req -sha256 -in customer-intermediate-ca.csr \
  -CA root-ca.crt -CAkey root-ca.key -CAcreateserial \
  -out customer-intermediate-ca.crt -days 1095 \
  -extfile <(cat <<CONF
basicConstraints = critical, CA:TRUE, pathlen:0
keyUsage = critical, keyCertSign, cRLSign
CONF
)
```

**4. Issue a Relay Server Certificate:**

```bash
# Generate relay key
openssl ecparam -genkey -name prime256v1 -out relay.key

# Create CSR
openssl req -new -sha256 -key relay.key \
  -out relay.csr \
  -subj "/C=US/O=AtlasShare/CN=relay.atlasshare.io"

# Sign with Relay Intermediate CA (90 days)
openssl x509 -req -sha256 -in relay.csr \
  -CA relay-intermediate-ca.crt -CAkey relay-intermediate-ca.key \
  -CAcreateserial -out relay.crt -days 90 \
  -extfile <(cat <<CONF
basicConstraints = critical, CA:FALSE
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = DNS:relay.atlasshare.io, DNS:localhost
CONF
)
```

**5. Issue a Customer Agent Certificate:**

```bash
CUSTOMER_ID="customer-a1b2c3d4"

# Generate agent key
openssl ecparam -genkey -name prime256v1 -out ${CUSTOMER_ID}.key

# Create CSR
openssl req -new -sha256 -key ${CUSTOMER_ID}.key \
  -out ${CUSTOMER_ID}.csr \
  -subj "/C=US/O=AtlasShare/CN=${CUSTOMER_ID}"

# Sign with Customer Intermediate CA (90 days)
openssl x509 -req -sha256 -in ${CUSTOMER_ID}.csr \
  -CA customer-intermediate-ca.crt -CAkey customer-intermediate-ca.key \
  -CAcreateserial -out ${CUSTOMER_ID}.crt -days 90 \
  -extfile <(cat <<CONF
basicConstraints = critical, CA:FALSE
keyUsage = critical, digitalSignature
extendedKeyUsage = clientAuth
CONF
)
```

---

## Production CA Options

For production deployments, use a proper CA infrastructure instead of manual OpenSSL commands.

### Option 1: step-ca (Smallstep)

[step-ca](https://smallstep.com/docs/step-ca/) is an open-source online CA that supports ACME, mTLS, and automated certificate management.

**Advantages:**
- ACME protocol support for automated issuance
- Built-in certificate templates
- Active community, production-proven
- Supports HSM key storage

**Setup:**
```bash
# Initialize CA
step ca init --name "AtlasShare CA" --dns ca.atlasshare.io

# Issue certificates via ACME or CLI
step ca certificate "relay.atlasshare.io" relay.crt relay.key
step ca certificate "customer-a1b2c3d4" agent.crt agent.key --not-after 2160h
```

### Option 2: HashiCorp Vault PKI

[Vault PKI Secrets Engine](https://developer.hashicorp.com/vault/docs/secrets/pki) provides a full-featured CA with access control, audit logging, and dynamic certificate issuance.

**Advantages:**
- Centralized secret management
- Detailed audit logging
- Dynamic short-lived certificates
- Integration with many infrastructure tools

**Setup:**
```bash
# Enable PKI engine
vault secrets enable pki

# Configure root CA
vault write pki/root/generate/internal \
  common_name="AtlasShare Root CA" ttl=87600h

# Configure intermediate CA
vault secrets enable -path=pki_relay pki
vault write pki_relay/intermediate/generate/internal \
  common_name="AtlasShare Relay CA" ttl=26280h

# Issue certificates
vault write pki_relay/issue/relay-cert \
  common_name="relay.atlasshare.io" ttl=2160h
```

### Option 3: cfssl (Cloudflare)

[cfssl](https://github.com/cloudflare/cfssl) is a lightweight CA toolkit suitable for smaller deployments.

**Advantages:**
- Simple JSON configuration
- HTTP API for issuance
- Lightweight, easy to deploy

**Setup:**
```bash
# Initialize CA
cfssl gencert -initca ca-csr.json | cfssljson -bare ca

# Issue certificate
cfssl gencert -ca ca.pem -ca-key ca-key.pem \
  -config config.json -profile server relay-csr.json | cfssljson -bare relay
```

### Recommendation

| Deployment Size | Recommended CA |
|----------------|---------------|
| Development / testing | OpenSSL scripts |
| Small production (< 50 agents) | cfssl or step-ca |
| Medium production (50-500 agents) | step-ca |
| Large production (500+ agents) | Vault PKI or step-ca with HSM |

---

## Issuance Workflow

### New Customer Onboarding

1. Generate a unique customer UUID.
2. Create a certificate signing request (CSR) with `CN=customer-{uuid}`.
3. Submit the CSR to the CA (step-ca, Vault, or manual signing).
4. Sign the CSR with the Customer Intermediate CA.
5. Deliver the signed certificate and CA bundle to the customer agent.
6. Record the certificate serial number, customer ID, and expiry date in the certificate inventory.

### Certificate Rotation (Community Edition)

The community edition supports file-based hot-reload: both the relay and agent poll their cert files (default: every 24 hours) and automatically reload when the content changes (SHA-256 fingerprint comparison). No process restart is needed.

**Operator workflow:**

1. Generate a new cert+key pair using the same CA that signed the original.
2. Create a new chain file: `cat new-leaf.crt intermediate-ca.crt > new-chain.crt`.
3. Replace the chain file and key on disk at the paths specified in the config (`cert_file`, `key_file`).
4. Wait up to 24 hours for the next poll cycle, or restart the service for immediate pickup.

**Automated renewal via CA API** (step-ca ACME, Vault PKI `/issue` endpoint) is supported by the enterprise edition. The community edition does not have an automated renewal agent -- cert rotation is operator-initiated.

To generate a new agent cert for an existing customer without regenerating the full CA hierarchy, use the `ats certs issue` command from `atlax-tools`:

```bash
ats certs issue
# Prompts for cert dir, customer ID, validity. Signs against existing Customer CA.
```

---

## Revocation

The community edition does not implement CRL fetching or OCSP checking. Revocation is enforced by short-lived certificates (90-day default) and operator-initiated disconnection via the admin API.

**Practical revocation procedure (community):**

1. Identify the compromised agent by customer ID.
2. Force-disconnect via the admin API:
   ```bash
   curl -X DELETE http://127.0.0.1:9090/agents/{customerID}
   ```
3. Do not re-issue a cert for that customer until the compromise is resolved.
4. The short 90-day validity limits the exposure window.

For CRL/OCSP enforcement, use the enterprise edition with step-ca or Vault PKI.

---

## Emergency Rotation

Use this procedure when a private key is known or suspected to be compromised.

### Agent Certificate Compromise

1. **Immediately revoke** the compromised certificate.
2. **Disconnect the agent** from the relay via the control plane API.
3. **Generate a new key pair** on the agent node (do not reuse the compromised key).
4. **Issue a new certificate** through the CA with the new public key.
5. **Deploy the new certificate** to the agent and restart.
6. **Verify** the agent connects successfully with the new certificate.
7. **Audit** relay logs for any suspicious activity during the compromise window.

### Relay Certificate Compromise

1. **Revoke** the compromised relay certificate.
2. **Generate a new key pair** for the relay.
3. **Issue a new relay certificate** from the Relay Intermediate CA.
4. **Deploy the new certificate** to the relay.
5. **Restart the relay** with GOAWAY to drain existing connections gracefully.
6. Agents will reconnect and verify the new relay certificate against their pinned CA.
7. **Audit** all connections during the compromise window.

### Intermediate CA Compromise

This is a critical incident requiring immediate escalation.

1. **Revoke the intermediate CA certificate** at the Root CA level.
2. **Generate a new intermediate CA** with a new key pair.
3. **Re-issue all leaf certificates** signed by the compromised intermediate.
4. **Deploy new certificates** to all affected relays or agents.
5. **Update the relay's trusted CA bundle** to include only the new intermediate CA.
6. **Conduct a full security audit** of all systems that used the compromised CA.

---

## Certificate Inventory

Maintain a record of all issued certificates for tracking, auditing, and rotation planning.

### Required Fields

| Field | Description |
|-------|-------------|
| Serial Number | Unique certificate serial number |
| Customer ID | `CN` from the certificate subject |
| Issuing CA | Which intermediate CA signed it |
| Not Before | Certificate validity start date |
| Not After | Certificate validity end date |
| Key Algorithm | EC P-256, RSA 2048, etc. |
| Status | Active, Revoked, Expired |
| Issued By | Operator or automation system that requested issuance |
| Revocation Date | Date of revocation (if applicable) |
| Revocation Reason | Reason for revocation (if applicable) |

### Storage

- For small deployments: a version-controlled JSON or YAML file
- For production: the CA's built-in certificate database (Vault, step-ca)
- For audit compliance: export to an append-only audit log

---

## Monitoring

### Certificate Expiry Monitoring

Use the Prometheus blackbox exporter or a dedicated certificate monitoring tool to track expiry dates.

```yaml
# Prometheus alerting rule for certificate expiry
groups:
  - name: certificate_expiry
    rules:
      - alert: AtlaxCertExpiringSoon
        expr: (probe_ssl_earliest_cert_expiry - time()) / 86400 < 30
        for: 1h
        labels:
          severity: warning
        annotations:
          summary: "Certificate expiring within 30 days"
          description: "Certificate for {{ $labels.instance }} expires in {{ $value | humanizeDuration }}."

      - alert: AtlaxCertExpiringCritical
        expr: (probe_ssl_earliest_cert_expiry - time()) / 86400 < 7
        for: 1h
        labels:
          severity: critical
        annotations:
          summary: "Certificate expiring within 7 days"
          description: "Certificate for {{ $labels.instance }} expires in {{ $value | humanizeDuration }}. Immediate renewal required."
```

### CA Health Monitoring

- Monitor the CA service availability (step-ca, Vault) with health checks
- Alert if the CA is unreachable, as this blocks certificate issuance and renewal
- Monitor CRL distribution freshness (CRL should be regenerated at least daily)

### Operational Checks

Run periodic certificate health checks:

```bash
# Check all certificates in the inventory
for cert in /etc/atlax/certs/*.crt; do
  expiry=$(openssl x509 -in "$cert" -noout -enddate | cut -d= -f2)
  subject=$(openssl x509 -in "$cert" -noout -subject | cut -d= -f6)
  echo "$subject: expires $expiry"
done
```
