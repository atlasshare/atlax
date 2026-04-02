# Docker Deployment Guide

## Overview

Production Docker images use multi-stage builds: Go compiler in the build stage, distroless runtime in the final image. The runtime image has no shell, no package manager, and runs as a non-root user.

## Building Images

```bash
# Build relay image
docker build -t atlax-relay:latest \
  -f deployments/docker/Dockerfile.relay \
  --build-arg VERSION=$(git describe --tags --always) \
  --build-arg COMMIT=$(git rev-parse HEAD) \
  .

# Build agent image
docker build -t atlax-agent:latest \
  -f deployments/docker/Dockerfile.agent \
  --build-arg VERSION=$(git describe --tags --always) \
  --build-arg COMMIT=$(git rev-parse HEAD) \
  .
```

## Running

### Relay

```bash
docker run -d \
  --name atlax-relay \
  -p 8443:8443 \
  -p 9090:9090 \
  -p 18080:18080 \
  -v /path/to/relay.yaml:/etc/atlax/relay.yaml:ro \
  -v /path/to/certs:/etc/atlax/certs:ro \
  atlax-relay:latest \
  -config /etc/atlax/relay.yaml
```

### Agent

```bash
docker run -d \
  --name atlax-agent \
  -v /path/to/agent.yaml:/etc/atlax/agent.yaml:ro \
  -v /path/to/certs:/etc/atlax/certs:ro \
  atlax-agent:latest \
  -config /etc/atlax/agent.yaml
```

The agent needs network access to local services. Use `--network host` if forwarding to services on the Docker host:

```bash
docker run -d \
  --name atlax-agent \
  --network host \
  -v /path/to/agent.yaml:/etc/atlax/agent.yaml:ro \
  -v /path/to/certs:/etc/atlax/certs:ro \
  atlax-agent:latest \
  -config /etc/atlax/agent.yaml
```

## Docker Compose (local development)

```yaml
services:
  relay:
    build:
      context: .
      dockerfile: deployments/docker/Dockerfile.relay
    ports:
      - "8443:8443"
      - "9090:9090"
      - "18080:18080"
    volumes:
      - ./relay.yaml:/etc/atlax/relay.yaml:ro
      - ./certs:/etc/atlax/certs:ro

  agent:
    build:
      context: .
      dockerfile: deployments/docker/Dockerfile.agent
    network_mode: host
    volumes:
      - ./agent.yaml:/etc/atlax/agent.yaml:ro
      - ./certs:/etc/atlax/certs:ro
    depends_on:
      - relay

  echo:
    image: alpine/socat
    command: TCP-LISTEN:9999,reuseaddr,fork EXEC:cat
    ports:
      - "9999:9999"
```

## Image Details

| Property | Value |
|----------|-------|
| Base image | `gcr.io/distroless/static-debian12:nonroot` |
| User | nonroot (UID 65534) |
| Shell | None (distroless) |
| Package manager | None |
| CA certificates | Copied from build stage |
| Size | ~10-15 MB (binary + ca-certs) |

## Security

- **No shell access:** distroless images have no shell. `docker exec` with `/bin/sh` will fail. This is intentional -- reduces attack surface.
- **Non-root:** runs as UID 65534 (nonroot). Cannot bind to privileged ports inside the container (use Docker port mapping instead).
- **Read-only volumes:** config and certs mounted as `:ro`.
- **No HEALTHCHECK in Dockerfile:** distroless has no `wget`/`curl`. Use Docker's external health check or the relay's `/healthz` endpoint from a monitoring system.
