# Prerequisite: Verify Dev Certificates

## Why

Phase 2 implements mTLS authentication. Integration tests require a valid certificate chain (Root CA, Relay Intermediate CA, Customer Intermediate CA, leaf certs). These were generated in Phase 1 prereqs but may have expired or been deleted.

## Steps

### 1. Check that certs/ directory exists

```bash
cd ~/projects/atlax
ls certs/
# Should contain: root-ca.pem, root-ca-key.pem, relay-ca.pem, relay-ca-key.pem,
#                 customer-ca.pem, customer-ca-key.pem, relay.pem, relay-key.pem,
#                 customer-dev-001.pem, customer-dev-001-key.pem
```

If missing, regenerate:

```bash
make certs-dev
# Or: bash scripts/gen-certs.sh
```

### 2. Verify certificate chains are valid

```bash
openssl verify -CAfile certs/root-ca.pem -untrusted certs/relay-ca.pem certs/relay.pem
# Expected: certs/relay.pem: OK

openssl verify -CAfile certs/root-ca.pem -untrusted certs/customer-ca.pem certs/customer-dev-001.pem
# Expected: certs/customer-dev-001.pem: OK
```

### 3. Verify leaf certs have not expired

```bash
openssl x509 -in certs/relay.pem -noout -dates
# notAfter should be in the future

openssl x509 -in certs/customer-dev-001.pem -noout -dates
# notAfter should be in the future
```

If expired, regenerate with `make certs-dev`.

### 4. Verify cert subjects

```bash
openssl x509 -in certs/relay.pem -noout -subject
# Expected: subject=CN = relay.atlax.local, O = AtlasShare

openssl x509 -in certs/customer-dev-001.pem -noout -subject
# Expected: subject=CN = customer-dev-001, O = AtlasShare
```

### 5. Confirm certs are still gitignored

```bash
git status
# certs/ should NOT appear as untracked
```

## Done When

- `certs/` directory contains all CA and leaf certificates
- `openssl verify` passes for both chains
- Leaf certs are not expired
- CN subjects match expected format
