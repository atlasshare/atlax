#!/usr/bin/env bash
# gen-certs.sh - Generate development certificates for atlax
#
# Creates a complete mTLS certificate hierarchy:
#   Root CA (10y) -> Relay Intermediate CA (3y) -> relay cert (90d)
#   Root CA (10y) -> Customer Intermediate CA (3y) -> agent cert (90d)
#
# Usage: CERT_OUTPUT_DIR=./certs bash scripts/gen-certs.sh

set -euo pipefail

CERT_DIR="${CERT_OUTPUT_DIR:-./certs}"

echo "Generating development certificates in: ${CERT_DIR}"
echo "---"

mkdir -p "${CERT_DIR}"

# ============================================================================
# Root CA (10-year validity, RSA 4096)
# ============================================================================

echo "Generating Root CA..."
openssl genrsa -out "${CERT_DIR}/root-ca.key" 4096 2>/dev/null

openssl req -new -x509 \
    -key "${CERT_DIR}/root-ca.key" \
    -out "${CERT_DIR}/root-ca.crt" \
    -days 3650 \
    -subj "/C=US/O=AtlasShare/CN=atlax-root-ca" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign"

echo "  Root CA: ${CERT_DIR}/root-ca.crt (10-year validity)"

# ============================================================================
# Relay Intermediate CA (3-year validity, RSA 4096, signed by Root CA)
# ============================================================================

echo "Generating Relay Intermediate CA..."
openssl genrsa -out "${CERT_DIR}/relay-ca.key" 4096 2>/dev/null

openssl req -new \
    -key "${CERT_DIR}/relay-ca.key" \
    -out "${CERT_DIR}/relay-ca.csr" \
    -subj "/C=US/O=AtlasShare/CN=atlax-relay-ca"

openssl x509 -req \
    -in "${CERT_DIR}/relay-ca.csr" \
    -CA "${CERT_DIR}/root-ca.crt" \
    -CAkey "${CERT_DIR}/root-ca.key" \
    -CAcreateserial \
    -out "${CERT_DIR}/relay-ca.crt" \
    -days 1095 \
    -extfile <(printf "basicConstraints=critical,CA:TRUE,pathlen:0\nkeyUsage=critical,keyCertSign,cRLSign") \
    2>/dev/null

echo "  Relay CA: ${CERT_DIR}/relay-ca.crt (3-year validity)"

# ============================================================================
# Customer Intermediate CA (3-year validity, RSA 4096, signed by Root CA)
# ============================================================================

echo "Generating Customer Intermediate CA..."
openssl genrsa -out "${CERT_DIR}/customer-ca.key" 4096 2>/dev/null

openssl req -new \
    -key "${CERT_DIR}/customer-ca.key" \
    -out "${CERT_DIR}/customer-ca.csr" \
    -subj "/C=US/O=AtlasShare/CN=atlax-customer-ca"

openssl x509 -req \
    -in "${CERT_DIR}/customer-ca.csr" \
    -CA "${CERT_DIR}/root-ca.crt" \
    -CAkey "${CERT_DIR}/root-ca.key" \
    -CAcreateserial \
    -out "${CERT_DIR}/customer-ca.crt" \
    -days 1095 \
    -extfile <(printf "basicConstraints=critical,CA:TRUE,pathlen:0\nkeyUsage=critical,keyCertSign,cRLSign") \
    2>/dev/null

echo "  Customer CA: ${CERT_DIR}/customer-ca.crt (3-year validity)"

# ============================================================================
# Relay server certificate (90-day validity, RSA 2048, signed by Relay CA)
# ============================================================================

echo "Generating relay server certificate..."
openssl genrsa -out "${CERT_DIR}/relay.key" 2048 2>/dev/null

openssl req -new \
    -key "${CERT_DIR}/relay.key" \
    -out "${CERT_DIR}/relay.csr" \
    -subj "/C=US/O=AtlasShare/CN=relay.atlax.local"

openssl x509 -req \
    -in "${CERT_DIR}/relay.csr" \
    -CA "${CERT_DIR}/relay-ca.crt" \
    -CAkey "${CERT_DIR}/relay-ca.key" \
    -CAcreateserial \
    -out "${CERT_DIR}/relay.crt" \
    -days 90 \
    -extfile <(printf "basicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:relay.atlax.local,DNS:localhost,IP:127.0.0.1") \
    2>/dev/null

echo "  Relay cert: ${CERT_DIR}/relay.crt (90-day validity, SAN: relay.atlax.local, localhost, 127.0.0.1)"

# ============================================================================
# Agent client certificate (90-day validity, RSA 2048, signed by Customer CA)
# ============================================================================

echo "Generating agent client certificate..."
openssl genrsa -out "${CERT_DIR}/agent.key" 2048 2>/dev/null

openssl req -new \
    -key "${CERT_DIR}/agent.key" \
    -out "${CERT_DIR}/agent.csr" \
    -subj "/C=US/O=AtlasShare/CN=customer-dev-001"

openssl x509 -req \
    -in "${CERT_DIR}/agent.csr" \
    -CA "${CERT_DIR}/customer-ca.crt" \
    -CAkey "${CERT_DIR}/customer-ca.key" \
    -CAcreateserial \
    -out "${CERT_DIR}/agent.crt" \
    -days 90 \
    -extfile <(printf "basicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=clientAuth") \
    2>/dev/null

echo "  Agent cert: ${CERT_DIR}/agent.crt (90-day validity, CN=customer-dev-001)"

# ============================================================================
# Certificate chain files
# ============================================================================

echo "Creating certificate chain files..."

cat "${CERT_DIR}/relay.crt" "${CERT_DIR}/relay-ca.crt" > "${CERT_DIR}/relay-chain.crt"
echo "  Relay chain: ${CERT_DIR}/relay-chain.crt"

cat "${CERT_DIR}/agent.crt" "${CERT_DIR}/customer-ca.crt" > "${CERT_DIR}/agent-chain.crt"
echo "  Agent chain: ${CERT_DIR}/agent-chain.crt"

cat "${CERT_DIR}/relay-ca.crt" "${CERT_DIR}/customer-ca.crt" > "${CERT_DIR}/intermediate-cas.crt"
echo "  Intermediate CAs: ${CERT_DIR}/intermediate-cas.crt"

# ============================================================================
# Clean up CSR files
# ============================================================================

rm -f "${CERT_DIR}"/*.csr
echo ""
echo "Cleaned up CSR files."

# ============================================================================
# Summary
# ============================================================================

echo ""
echo "=== Certificate Generation Summary ==="
echo ""
echo "Root CA:"
echo "  Certificate: ${CERT_DIR}/root-ca.crt"
echo "  Key:         ${CERT_DIR}/root-ca.key"
echo ""
echo "Relay Intermediate CA:"
echo "  Certificate: ${CERT_DIR}/relay-ca.crt"
echo "  Key:         ${CERT_DIR}/relay-ca.key"
echo ""
echo "Customer Intermediate CA:"
echo "  Certificate: ${CERT_DIR}/customer-ca.crt"
echo "  Key:         ${CERT_DIR}/customer-ca.key"
echo ""
echo "Relay Server (signed by Relay CA):"
echo "  Certificate: ${CERT_DIR}/relay.crt"
echo "  Chain:       ${CERT_DIR}/relay-chain.crt"
echo "  Key:         ${CERT_DIR}/relay.key"
echo "  CN:          relay.atlax.local"
echo "  SAN:         DNS:relay.atlax.local, DNS:localhost, IP:127.0.0.1"
echo ""
echo "Agent Client (signed by Customer CA):"
echo "  Certificate: ${CERT_DIR}/agent.crt"
echo "  Chain:       ${CERT_DIR}/agent-chain.crt"
echo "  Key:         ${CERT_DIR}/agent.key"
echo "  CN:          customer-dev-001"
echo ""
echo "Chain Files:"
echo "  Relay chain:       ${CERT_DIR}/relay-chain.crt"
echo "  Agent chain:       ${CERT_DIR}/agent-chain.crt"
echo "  Intermediate CAs:  ${CERT_DIR}/intermediate-cas.crt"
echo ""
echo "NOTE: These certificates are for development only."
echo "      Production certificates should use a proper CA (step-ca, Vault PKI, cfssl)."
echo "      Production agent certs use CN=customer-{uuid} format."
