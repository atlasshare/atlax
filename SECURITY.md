# Security Policy

## Reporting a Vulnerability

We take the security of atlax seriously. If you discover a security vulnerability, please report it responsibly.

**Email:** security@atlasshare.io

Please include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

## Response Timeline

- **Acknowledgement:** Within 48 hours of report
- **Initial assessment:** Within 5 business days
- **Fix timeline:** Based on severity, typically within 30 days
- **Public disclosure:** 90 days after report (coordinated with reporter)

## Scope

The following components are in scope for security reports:

- **Relay server** (`atlax-relay`) - TLS listener, agent management, client routing
- **Tunnel agent** (`atlax-agent`) - Connection management, local service forwarding
- **Wire protocol** - Frame parsing, stream multiplexing, flow control
- **Authentication** - mTLS implementation, certificate validation, identity extraction

## Out of Scope

- Third-party dependencies (report via Dependabot or upstream maintainers)
- Denial of service via resource exhaustion (covered by rate limiting)
- Issues in example/test configurations
- Social engineering attacks

## Disclosure Policy

We follow coordinated disclosure. We will:
1. Confirm the vulnerability and determine its impact
2. Develop and test a fix
3. Release a patch and advisory
4. Credit the reporter (unless anonymity is requested)
