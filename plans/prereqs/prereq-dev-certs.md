# Prerequisite: Generate Dev Certificates

## Why

While Phase 1 (Core Protocol) does not use TLS directly, generating dev certificates now ensures the `scripts/gen-certs.sh` script works and the certificate infrastructure is ready for Phase 2 (Agent) integration tests. Catching cert generation issues early avoids blocking Phase 2.

## Steps

### 1. Check that OpenSSL is available

```bash
openssl version
# Should show OpenSSL 3.x or LibreSSL 3.x
```

If not installed:

```bash
# macOS (LibreSSL is pre-installed, but if you need OpenSSL 3):
brew install openssl@3
```

### 2. Review the cert generation script

```bash
cat scripts/gen-certs.sh
```

Understand what it generates:
- Root CA (10-year validity)
- Relay Intermediate CA (3-year)
- Customer Intermediate CA (3-year)
- Relay server cert (90-day, CN=relay.atlax.local)
- Customer agent cert (90-day, CN=customer-dev-001)

### 3. Run the script

```bash
cd ~/projects/atlax
make certs-dev
# Or directly:
bash scripts/gen-certs.sh
```

### 4. Verify generated certificates

```bash
ls -la certs/
# Should contain: root-ca.pem, root-ca-key.pem, relay-ca.pem, relay-ca-key.pem,
#                 customer-ca.pem, customer-ca-key.pem, relay.pem, relay-key.pem,
#                 customer-dev-001.pem, customer-dev-001-key.pem

# Verify cert chain
openssl verify -CAfile certs/root-ca.pem -untrusted certs/relay-ca.pem certs/relay.pem
# Should output: certs/relay.pem: OK

openssl verify -CAfile certs/root-ca.pem -untrusted certs/customer-ca.pem certs/customer-dev-001.pem
# Should output: certs/customer-dev-001.pem: OK
```

### 5. Confirm certs are gitignored

```bash
git status
# certs/ should NOT appear as untracked files
grep "certs/" .gitignore
# Should show certs/ is in .gitignore
```

## Troubleshooting

**"Permission denied" on gen-certs.sh:**
```bash
chmod +x scripts/gen-certs.sh
```

**"openssl: command not found":**
```bash
# macOS
brew install openssl@3
echo 'export PATH="/opt/homebrew/opt/openssl@3/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**Script fails with "directory already exists":**
```bash
rm -rf certs/
make certs-dev
```

## Done When

- `certs/` directory contains all CA and leaf certificates
- `openssl verify` passes for both relay and customer cert chains
- `git status` does not show certs/ as untracked
