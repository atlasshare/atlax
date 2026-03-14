# Networking & Remote Access Architecture

**Product:** AtlasShare (repo: `atlasshare-sg`)
**Deployment assumption:** multiple tenants under **one domain**
**Primary objective:** enable secure remote work under CGNAT/ISP constraints without exposing SMB to the internet

---

## 1. Goals

Networking and remote access must:

- Work reliably behind **CGNAT** and dynamic home/SMB ISP environments
- Support secure remote work without requiring inbound ports on customer networks
- Avoid exposing **SMB** over the public internet
- Enforce **zero-trust** (authn + tenant resolution + authz on every request)
- Support offline / air-gapped deployments (with explicit degraded modes)
- Be portable across:
  - single-node appliance
  - on-prem servers
  - private cloud VMs
  - hybrid (edge + core)
- Avoid vendor lock-in (relay components should be self-hostable)

---

## 2. Non-goals (v1)

- Public-facing SMB access (SMB remains LAN/internal-only)
- Building a proprietary VPN protocol
- Mandatory cloud dependency for core functionality
- Replacing enterprise-grade SD-WAN/zero-trust products (we integrate with them)

---

## 3. Threat model summary (network-specific)

Assume:

- Remote users are on untrusted networks
- ISP may use CGNAT and block inbound ports
- Attackers will probe the public relay surface
- Credentials may be phished; sessions may be hijacked
- Misconfiguration is common in SMB environments

Network controls must prioritize:

- minimal exposed surface
- strong authentication
- short-lived access capabilities
- auditable access
- rate limiting and abuse controls

---

## 4. Recommended access pattern: HTTPS-first control plane

### 4.1 Core rule

**All remote access uses HTTPS to AtlasShare APIs**.

- Remote clients (Web UI / Sync client / Mobile) talk to:
  - `https://drive.example.com` (single domain)
- The AtlasShare backend enforces:
  - authentication (OIDC)
  - tenant resolution
  - authorization
  - audit logging

### 4.2 SMB guidance

SMB is supported for local networks only (e.g., office LAN, VPN inside perimeter).
Remote SMB over internet is discouraged and out-of-scope for secure-by-default posture.

If customers insist on remote SMB, the recommended enterprise approach is:

- corporate VPN / ZTNA overlay to bring the user into the private network
- SMB then behaves like LAN SMB (still risky but controlled by customer)

AtlasShare will not design “SMB over public internet” as a default feature.

---

## 5. Remote connectivity under CGNAT: Relay-first model

Because CGNAT breaks inbound connections, AtlasShare must support a **relay** that the customer node can *dial out to*.

### 5.1 Components

- **Customer Node**: the on-prem AtlasShare instance (appliance/server)
- **Relay Node**: a VPS-hosted gateway that accepts inbound internet traffic
- **Clients**: browser, sync client, mobile app

### 5.2 High-level flow

```
Client  --->  Relay (public)  <--- outbound tunnel ---  Customer Node
           HTTPS/WSS                         (no inbound needed)
```

### 5.3 What the relay does (strictly limited)

- Terminates public TLS (or passes through)
- Routes traffic to the correct customer node via a secure tunnel
- Applies coarse protections:
  - rate limiting
  - WAF-like rules (optional)
  - DDoS-aware posture (provider dependent)
- Does **not** contain tenant data
- Does **not** bypass AtlasShare authz
- Does **not** expose SMB

The relay is a transport and routing layer, not a business layer.

---

## 6. Tunnel options (choose based on operational requirements)

AtlasShare should support at least one self-hosted tunnel approach and allow integration with third-party overlays.

### Option A (recommended for v1): WireGuard + persistent outbound tunnel

- Customer node establishes WireGuard tunnel to Relay
- Relay routes `drive.example.com` traffic over the tunnel to the customer node
- Works behind CGNAT because customer initiates the connection

Notes:

- This is similar to a “site-to-site” tunnel but initiated outbound
- Requires relay VPS with a public IP
- Customer node does not need public inbound ports

Pros:

- Simple, fast, secure
- Self-hostable
- No vendor lock-in

Cons:

- Needs careful routing and firewalling
- Certificate/domain routing must be designed well

### Option B: Reverse tunnel (TLS) agent

- Customer node runs an agent that creates a persistent outbound TLS tunnel to relay
- Relay multiplexes HTTP/WSS streams back to the node

Pros:

- Works without kernel WireGuard availability
- Easy NAT traversal
- Very “SaaS-like” user experience

Cons:

- Requires building/maintaining tunnel agent (more code)
- Still needs strong security boundaries

### Option C: Third-party ZTNA / overlay integration

Examples:

- Tailscale, Cloudflare Zero Trust, Zscaler, etc.

Pros:

- Leverages mature products
- Good for customers already invested

Cons:

- Vendor lock-in (avoid making this required)
- Some are not self-hostable

**AtlasShare posture:** integrations allowed, never required.

---

## 7. Domain & TLS strategy (single domain, many tenants)

### 7.1 Domain

Use one domain for the platform UI and API:

- `https://drive.example.com`

Tenants are resolved post-authentication, not by DNS.

### 7.2 TLS termination

Supported patterns:

1. **Relay terminates TLS** (recommended for simplicity)
   - Relay has public certificate for `drive.example.com`
   - Relay forwards traffic over tunnel to node
2. **TLS passthrough** to customer node (advanced)
   - Customer node holds cert
   - Relay only routes raw TCP stream

v1 recommendation:

- Terminate TLS at relay, then secure relay→node with the tunnel (WireGuard or TLS tunnel)
- Keep an option to terminate at node for air-gapped / internal-only deployments

### 7.3 Certificates

For production:

- Let’s Encrypt (public reachable relay)
- Or enterprise PKI (customer managed)
For offline/air-gapped:
- internal CA + manual distribution

---

## 8. Networking segmentation: control plane vs data plane

### 8.1 Control plane endpoints

- Authentication endpoints
- Tenant selection / resolution endpoints
- Metadata APIs (listing, sharing, permissions)
- Admin APIs
- Audit APIs

All over HTTPS.

### 8.2 Data plane endpoints (file bytes)

- Upload/download endpoints
- Streaming endpoints

Prefer:

- scoped, short-lived access tokens
- signed URLs if storage backend supports it later
- backend streaming fallback for offline

---

## 9. Remote access UX (what users experience)

### 9.1 Web UI

- User visits `drive.example.com`
- OIDC sign-in
- Tenant resolved via claim or selector
- Access granted per RBAC + policy

### 9.2 Sync client

- Uses OIDC device flow or PKCE flow (depending on platform)
- Maintains refresh token securely
- Performs chunked uploads/downloads over HTTPS
- Uses backoff and resumes on network interruption

### 9.3 Mobile app

- Same as web, with mobile-friendly OIDC flows
- Offline browsing of cached metadata (policy-controlled)

---

## 10. Firewall & port requirements (default posture)

### 10.1 Customer node (behind NAT)

Inbound:

- none required for remote access
Outbound:
- to relay (WireGuard UDP, or TLS tunnel TCP)
- to IdP endpoints (OIDC)
- optional: email/notification provider

### 10.2 Relay node (public VPS)

Inbound:

- 443/tcp (HTTPS)
- optionally 80/tcp (ACME HTTP-01)
- WireGuard UDP port (if using WireGuard option)
Outbound:
- tunnel backhaul to customer node

### 10.3 Local/LAN access

Customer may expose local services:

- 443 on LAN for internal access
- SMB on LAN only (if enabled)

---

## 11. Security controls for relay exposure

Relay should enforce:

- rate limiting (per IP, per path)
- basic WAF rules (block obvious abuse)
- strict TLS (modern ciphers, HSTS)
- request size limits
- connection limits
- abuse monitoring

AtlasShare backend still enforces:

- authn/authz
- tenant isolation
- audit logging
- content integrity checks

Relay logs are operational, not audit records.

---

## 12. ISP realities and dynamic IP handling

Because home ISPs can change public IPs frequently:

- relay is the stable public endpoint
- customer node only needs outbound connectivity
- no dynamic DNS required for customer networks

This directly solves the “AT&T no static IPv4” problem.

---

## 13. Offline / air-gapped mode

In offline environments:

- No relay required
- AtlasShare accessible only on internal network
- OIDC may be:
  - internal IdP
  - or cached identity assertions (explicit degraded mode; limited time)

Remote work over the internet is out of scope in true air-gapped deployments by definition.

---

## 14. Multi-tenant MSP operation at the network layer

### 14.1 MSP control plane options

Two models:

**Model 1: MSP hosts relay + manages customer nodes**

- MSP provides relay infrastructure
- customer nodes dial out to MSP relay
- MSP offers managed updates and monitoring (policy-controlled)

**Model 2: Customer hosts relay**

- customer provides their own relay VPS
- AtlasShare supports it fully
- MSP assists with setup (consulting)

AtlasShare should support both, enabling consulting flexibility.

### 14.2 Tenant isolation at relay

Relay routing must never mix customers:

- per-customer tunnel identities/keys
- per-customer routing table entries
- strict ACLs (tunnel maps to node identity)

The relay should not interpret tenant/user data; it only routes to the correct node.

---

## 15. Implementation guidance (v1 pragmatic)

### v1 recommended path (lowest risk)

1. Implement HTTPS-first API/UI access
2. Implement relay VPS reference deployment
3. Implement WireGuard outbound tunnel from node to relay
4. Provide a “one command” installer (later) or Ansible playbook
5. Provide hardened firewall defaults and a diagnostics script

### Diagnostics must include

- tunnel status
- relay connectivity
- DNS resolution
- certificate validity
- basic API health checks
- latency and MTU checks (WireGuard common issue)

---

## 16. Open decisions (tracked)

- WireGuard vs reverse TLS tunnel as the default offering
- Whether relay is packaged as part of “enterprise subscription”
- How to handle multi-node clusters behind one relay (later)
- Whether to support QUIC/HTTP3 (later)

---

## 17. Summary

AtlasShare networking is designed around:

- **HTTPS-first access** for all remote clients
- **Relay-first remote connectivity** for CGNAT and dynamic IP environments
- **Outbound tunnels** (WireGuard or reverse TLS) so customers do not need inbound ports
- **No remote SMB exposure by default**
- **Zero-trust enforcement** at the backend API layer with complete auditing
