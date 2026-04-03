# Systemd Deployment Guide

## Overview

Hardened systemd service units are provided for both the relay and agent. They include security sandboxing (ProtectSystem, NoNewPrivileges, CapabilityBoundingSet), resource limits, and journal logging.

## Installation

### 1. Create the atlax system user

```bash
sudo useradd -r -s /sbin/nologin -d /nonexistent atlax
```

### 2. Create directories

```bash
sudo mkdir -p /etc/atlax /var/lib/atlax /var/log/atlax
sudo chown atlax:atlax /var/lib/atlax /var/log/atlax
```

### 3. Install binaries

```bash
sudo cp bin/atlax-relay /usr/local/bin/atlax-relay
sudo cp bin/atlax-agent /usr/local/bin/atlax-agent
sudo chmod 755 /usr/local/bin/atlax-relay /usr/local/bin/atlax-agent
```

### 4. Install configuration and certificates

```bash
sudo cp relay.yaml /etc/atlax/relay.yaml
sudo cp -r certs/ /etc/atlax/certs/
sudo chown -R atlax:atlax /etc/atlax
sudo chmod 600 /etc/atlax/certs/*.key
```

### 5. Install service units

```bash
sudo cp deployments/systemd/atlax-relay.service /etc/systemd/system/
sudo cp deployments/systemd/atlax-agent.service /etc/systemd/system/
sudo systemctl daemon-reload
```

### 6. Enable and start

```bash
# Relay:
sudo systemctl enable atlax-relay
sudo systemctl start atlax-relay
sudo systemctl status atlax-relay

# Agent:
sudo systemctl enable atlax-agent
sudo systemctl start atlax-agent
sudo systemctl status atlax-agent
```

## Environment Variable Overrides

Create `/etc/atlax/relay.env` or `/etc/atlax/agent.env`:

```bash
ATLAX_RELAY_ADDR=relay.example.com:8443
ATLAX_TLS_CERT=/etc/atlax/certs/relay.crt
ATLAX_LOG_LEVEL=debug
```

Environment variables override YAML config values.

## Logs

```bash
# Follow relay logs
sudo journalctl -u atlax-relay -f

# Last 100 lines
sudo journalctl -u atlax-relay -n 100

# Since boot
sudo journalctl -u atlax-relay -b

# JSON output for parsing
sudo journalctl -u atlax-relay -o json
```

## Updating Binaries

```bash
sudo systemctl stop atlax-relay
sudo cp bin/atlax-relay-new /usr/local/bin/atlax-relay
sudo systemctl start atlax-relay
```

## Security Hardening Details

The service units include:

| Directive | What it does |
|-----------|-------------|
| NoNewPrivileges | Process cannot gain new privileges via setuid/setgid |
| ProtectSystem=strict | Filesystem is read-only except explicit ReadWritePaths |
| ProtectHome=yes | /home is inaccessible |
| PrivateTmp=yes | Private /tmp, not shared with other services |
| PrivateDevices=yes | No access to physical devices |
| ProtectKernelTunables=yes | /proc/sys is read-only |
| ProtectKernelModules=yes | Cannot load kernel modules |
| ProtectControlGroups=yes | Cannot modify cgroups |
| RestrictNamespaces=yes | Cannot create new namespaces |
| MemoryDenyWriteExecute=yes | Cannot create writable+executable memory |
| LockPersonality=yes | Cannot change execution domain |
| ReadOnlyPaths=/etc/atlax | Config and certs are read-only to the process |
| CAP_NET_BIND_SERVICE | Relay only: allows binding to ports < 1024 |
