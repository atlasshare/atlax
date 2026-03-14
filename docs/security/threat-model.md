# Threat Model

## Overview

This document identifies the assets, threat actors, attack surfaces, and
mitigations for the atlax relay and agent system. The analysis follows the
STRIDE framework (Spoofing, Tampering, Repudiation, Information Disclosure,
Denial of Service, Elevation of Privilege) applied to both the relay and agent
components.

## Assets

| Asset | Location | Sensitivity | Description |
|-------|----------|-------------|-------------|
| Customer data in transit | TLS tunnel | High | Application-layer bytes (SMB files, HTTP responses) flowing through the tunnel. The relay forwards these as opaque byte streams. |
| Agent private key | Agent disk | Critical | The private key for the agent's mTLS certificate. Compromise allows impersonation of the customer. |
| Relay private key | Relay disk | Critical | The private key for the relay's TLS server certificate. Compromise allows impersonation of the relay. |
| Intermediate CA private keys | Offline/HSM | Critical | Keys used to sign leaf certificates. Compromise allows issuance of arbitrary certificates for any customer or relay. |
| Root CA private key | Offline/HSM | Critical | The ultimate trust anchor. Compromise undermines the entire PKI. |
| Agent Registry state | Relay memory | Medium | Maps customer identities to live connections. Corruption could cause misrouting. |
| Customer certificate (leaf) | Agent disk | Medium | The public certificate itself is not secret, but combined with the private key it enables authentication. |
| Relay configuration | Relay disk | Medium | Contains port-to-customer mappings, CA trust store paths, and operational parameters. |
| Agent configuration | Agent disk | Medium | Contains relay address, service mappings, and certificate paths. |

## Threat Actors

| Actor | Capability | Motivation |
|-------|------------|------------|
| External attacker (internet) | Network access to relay public ports, can probe and send arbitrary traffic | Data theft, service disruption, pivot into customer networks |
| Compromised relay | Full control of relay process and memory, can inspect forwarded traffic | Data interception, traffic manipulation, customer impersonation |
| Compromised agent | Full control of agent process, access to local network | Lateral movement within customer network, data exfiltration |
| Malicious insider (MSP operator) | Access to relay infrastructure, potentially to CA keys | Data access, customer impersonation, surveillance |
| Network-level attacker (ISP, MITM) | Can intercept, modify, or drop network traffic between agent and relay | Traffic interception, session hijacking, denial of service |

## Attack Scenarios and Mitigations

### 1. Agent Impersonation

**Scenario:** An attacker obtains or forges a customer agent certificate and
connects to the relay, gaining access to the customer's port allocation and
potentially intercepting or injecting traffic.

**Mitigations:**

- mTLS with TLS 1.3 prevents forged certificates (attacker would need the
  Customer Intermediate CA private key to sign a valid certificate).
- 90-day certificate validity limits the exposure window for a stolen
  certificate.
- CRL-based revocation allows immediate invalidation of compromised
  certificates.
- Relay logs all agent connections with certificate fingerprint for forensic
  analysis.

### 2. Relay Impersonation (Man-in-the-Middle)

**Scenario:** An attacker intercepts the agent's connection to the relay and
presents a fraudulent relay certificate, allowing traffic interception.

**Mitigations:**

- The agent validates the relay's certificate against the Relay Intermediate CA
  in its trust store.
- `ServerName` verification in the agent's TLS config prevents accepting
  certificates for other domains.
- TLS 1.3 handshake includes anti-downgrade protections.
- Optional certificate pinning provides defense-in-depth against CA compromise.

### 3. Cross-Tenant Traffic Routing

**Scenario:** An attacker exploits a bug in the relay's routing logic to access
another customer's agent or intercept their traffic.

**Mitigations:**

- Streams are scoped to individual agent connections. The wire protocol
  contains no mechanism to address streams on other connections.
- Port-to-customer mapping is static and loaded from configuration. A client
  connecting to port 10001 can only reach the customer assigned to that port.
- The relay verifies the customer ID extracted from the mTLS certificate before
  registering the agent.
- Integration tests specifically validate that cross-tenant routing is
  impossible.

### 4. Denial of Service Against Relay

**Scenario:** An attacker floods the relay with connections, SYN packets, or
malformed frames to exhaust resources and deny service to legitimate customers.

**Mitigations:**

- Per-customer connection and stream limits prevent a single customer (or
  attacker with a stolen certificate) from exhausting relay resources.
- Rate limiting on the TLS listener (connections per second per source IP).
- Maximum frame payload size (16MB) prevents memory exhaustion from oversized
  frames.
- Flow control windows cap memory consumption per stream and per connection.
- Graceful degradation: when limits are reached, the relay rejects new
  connections/streams but continues serving existing ones.
- OS-level protections: SYN cookies, connection limits via iptables/nftables.

### 5. Relay Compromise (Data Interception)

**Scenario:** An attacker gains access to the relay process or host, allowing
inspection of all forwarded traffic in memory.

**Mitigations:**

- The relay is a transport-only layer and does not log or store application
  data. However, an attacker with memory access can observe decrypted TLS
  traffic in transit.
- This is a fundamental limitation of any non-end-to-end encrypted relay
  architecture. To mitigate:
  - Harden the relay host (minimal services, OS security updates, restricted
    SSH access).
  - Use hardware security modules (HSMs) for private key storage.
  - Monitor relay host integrity (file integrity monitoring, intrusion
    detection).
  - Consider application-level encryption for highly sensitive data (the relay
    would forward ciphertext it cannot decrypt).
- Relay compromise does not grant access to other customers' data unless the
  attacker can also compromise the per-customer routing (which requires
  modifying configuration or exploiting a routing bug).

### 6. Agent Private Key Theft

**Scenario:** An attacker compromises the customer node and steals the agent's
private key and certificate.

**Mitigations:**

- Certificate files on disk have restrictive permissions (0600, owner only).
- 90-day validity limits the useful life of a stolen certificate.
- CRL-based revocation allows immediate invalidation once theft is detected.
- Key rotation on every certificate renewal (default behavior) means the old
  key becomes useless after the next rotation.
- Future: TPM-backed key storage would prevent key export entirely.

## STRIDE Analysis: Relay

| Threat | Category | Attack | Mitigation | Residual Risk |
|--------|----------|--------|------------|---------------|
| S1 | Spoofing | Attacker presents forged agent certificate | mTLS verification against Customer Intermediate CA; CRL check | CA key compromise (mitigated by offline storage) |
| T1 | Tampering | Attacker modifies frames in transit | TLS 1.3 authenticated encryption (AEAD) | Relay-level tampering if relay is compromised |
| T2 | Tampering | Attacker modifies relay configuration | File permissions, host hardening, config integrity checks | Compromised host with root access |
| R1 | Repudiation | Agent denies connecting | Audit log of all connections with certificate fingerprint, timestamp, source IP | Log tampering if relay host is compromised |
| I1 | Information Disclosure | Traffic interception on network | TLS 1.3 encryption with forward secrecy | Relay-level interception (data in memory) |
| I2 | Information Disclosure | Relay memory dump reveals customer data | Host hardening, minimal data retention in memory | Fundamental relay architecture limitation |
| D1 | Denial of Service | Connection flooding | Rate limiting, per-customer limits, OS-level protections | Volumetric DDoS (requires upstream mitigation) |
| D2 | Denial of Service | Malformed frame flooding | Frame validation, maximum payload size, connection termination on protocol error | CPU exhaustion from validation overhead (bounded by connection limits) |
| E1 | Elevation of Privilege | Attacker escalates from one customer to another | Port-based isolation, stream scoping, certificate-based identity | Relay software bug in routing logic |
| E2 | Elevation of Privilege | Attacker exploits relay process to gain host access | Minimal relay process privileges (non-root), seccomp/AppArmor profiles | Kernel vulnerability |

## STRIDE Analysis: Agent

| Threat | Category | Attack | Mitigation | Residual Risk |
|--------|----------|--------|------------|---------------|
| S1 | Spoofing | Attacker impersonates relay | Agent validates relay certificate against Relay Intermediate CA; ServerName check | Relay Intermediate CA compromise |
| T1 | Tampering | Attacker modifies update binary | ed25519 signature verification, SHA256 checksum, HTTPS download | Compromise of signing key |
| T2 | Tampering | Attacker modifies agent configuration | File permissions, host hardening | Compromised customer node with root access |
| R1 | Repudiation | Agent denies forwarding specific traffic | Agent-side audit logging of stream lifecycle events | Log tampering if agent node is compromised |
| I1 | Information Disclosure | Local service traffic intercepted on LAN | Agent forwards to 127.0.0.1 by default; service map validation | Agent compromise exposes local service traffic |
| I2 | Information Disclosure | Agent private key stolen from disk | Restrictive file permissions (0600), short-lived certificates | Full node compromise with root access |
| D1 | Denial of Service | Relay sends excessive STREAM_OPEN | Concurrent stream limits, service map validation | Stream creation CPU overhead |
| D2 | Denial of Service | Local service unresponsive, blocking agent | Per-stream timeouts, stream reset on timeout | Resource accumulation if many streams block simultaneously |
| E1 | Elevation of Privilege | Relay exploits tunnel to reach arbitrary local services | Service map validation (whitelist of allowed forward targets) | Agent software bug bypassing service map |
| E2 | Elevation of Privilege | Attacker pivots from agent to other LAN hosts | Firewall rules on agent node, service map restricted to localhost | Misconfiguration allowing non-localhost targets |

## Residual Risks

The following risks are acknowledged and accepted, with recommendations for
further mitigation:

| Risk | Severity | Mitigation Path |
|------|----------|-----------------|
| Relay can observe decrypted traffic in memory | High | Application-level encryption for sensitive data; consider end-to-end encryption overlay |
| CA private key compromise enables arbitrary certificate issuance | Critical | Offline HSM storage for Root CA; strict access control for Intermediate CA keys; monitoring for unauthorized certificate issuance |
| Volumetric DDoS against relay public IP | Medium | Upstream DDoS mitigation (cloud provider, Cloudflare); anycast IP if using multiple relays |
| Agent node compromise exposes local network | High | Network segmentation on customer LAN; agent runs with minimal OS privileges; service map restricted to explicit targets |
| Supply chain attack on agent binary | Medium | Reproducible builds; binary signing with pinned public key; update rollback on crash |
