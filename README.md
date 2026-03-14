# atlax

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)

A custom reverse TLS tunnel with TCP stream multiplexing, built in Go. Designed to bypass CGNAT by having customer nodes dial out to a relay with a public IP.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                           RELAY (VPS)                               │
│                                                                     │
│  ┌─────────────────┐    ┌─────────────────┐    ┌────────────────┐  │
│  │  TLS Listener   │    │  Agent Registry │    │ Client Listener│  │
│  │  (Agent conns)  │───▶│  (node mapping) │◀───│  (TCP ports)   │  │
│  └─────────────────┘    └─────────────────┘    └────────────────┘  │
│           │                      │                      │          │
│           │              ┌───────┴───────┐              │          │
│           └─────────────▶│   Mux Router  │◀─────────────┘          │
│                          └───────────────┘                         │
└─────────────────────────────────────────────────────────────────────┘
                                   │
                          TLS Tunnel (outbound)
                                   │
┌─────────────────────────────────────────────────────────────────────┐
│                        CUSTOMER NODE                                │
│                                                                     │
│  ┌─────────────────┐    ┌─────────────────┐    ┌────────────────┐  │
│  │  Tunnel Agent   │───▶│  Stream Demux   │───▶│ Local Services │  │
│  │  (dials relay)  │    │  (route by ID)  │    │ (Samba, HTTP)  │  │
│  └─────────────────┘    └─────────────────┘    └────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

**Data flow:** Customer node dials out to relay over mTLS. Relay accepts inbound client connections on service ports, opens multiplexed streams to the agent, and the agent forwards traffic to local services.

## Quick Start

See [docs/development/getting-started.md](docs/development/getting-started.md) for setup instructions.

## Community vs Enterprise

| Feature | Community | Enterprise |
|---------|:---------:|:----------:|
| Reverse TLS tunnel | x | x |
| TCP stream multiplexing | x | x |
| mTLS authentication | x | x |
| Custom wire protocol | x | x |
| In-memory agent registry | x | x |
| Structured audit logging | x | x |
| Distributed agent registry (Redis/etcd) | | x |
| Multi-relay clustering | | x |
| SIEM audit integration | | x |
| Web management dashboard | | x |
| Auto-scaling relay pools | | x |
| Priority support & SLA | | x |

## Documentation

- [Architecture](docs/architecture/) - System design and component overview
- [Protocol](docs/protocol/) - Wire protocol specification
- [Security](docs/security/) - Authentication, encryption, and threat model
- [Operations](docs/operations/) - Deployment, monitoring, and troubleshooting
- [Development](docs/development/) - Contributing, building, and testing
- [API](docs/api/) - Internal API reference
- [Reference](docs/reference/) - Original design documents

## Building

```bash
make build       # Build both binaries
make test        # Run tests with race detector
make lint        # Run linters
```

## Contributing

Contributions are welcome. Please read the development guide at [docs/development/](docs/development/) before submitting a pull request.

## Security

For security vulnerabilities, please see [SECURITY.md](SECURITY.md).

## License

Apache 2.0 - see [LICENSE](LICENSE) for details.
