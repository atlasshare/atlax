# Prometheus Monitoring Guide

## Overview

The atlax relay exposes Prometheus metrics on the admin port (default `:9090`) at the `/metrics` endpoint. Metrics are per-customer, enabling multi-tenant monitoring from a single relay.

## Setup

### 1. Verify the relay exposes metrics

```bash
curl http://localhost:9090/metrics | grep atlax
```

You should see counters and gauges prefixed with `atlax_`.

### 2. Configure Prometheus scrape

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: atlax-relay
    scrape_interval: 15s
    static_configs:
      - targets:
          - relay.example.com:9090
    # If admin port is only on localhost (behind Caddy):
    # Use SSH tunnel or run Prometheus on the same host
```

For relays with `admin_addr: 127.0.0.1:9090`, Prometheus must run on the same host or you need an SSH tunnel:

```bash
ssh -L 9090:127.0.0.1:9090 user@relay.example.com
```

### 3. Verify scraping

Open Prometheus UI (default `http://localhost:9090`) and query:

```promql
atlax_streams_active
```

## Available Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `atlax_streams_total` | Counter | customer_id | Total streams opened |
| `atlax_streams_active` | Gauge | customer_id | Currently active streams |
| `atlax_connections_total` | Counter | customer_id | Total agent connections |
| `atlax_connections_active` | Gauge | customer_id | Currently connected agents |
| `atlax_clients_rejected_total` | Counter | customer_id, reason | Rejected client connections |

Rejection reasons: `rate_limited`, `stream_limit`, `no_agent`.

## Useful Queries

### Active streams per customer

```promql
atlax_streams_active
```

### Stream open rate (per second)

```promql
rate(atlax_streams_total[5m])
```

### Rejection rate by reason

```promql
rate(atlax_clients_rejected_total[5m])
```

### Total connected agents

```promql
sum(atlax_connections_active)
```

### Customer with most active streams

```promql
topk(5, atlax_streams_active)
```

## Retention

For a single relay with 10 customers, metrics volume is minimal. Default Prometheus retention (15 days) is sufficient. For longer history, configure:

```yaml
# prometheus.yml
storage:
  tsdb:
    retention.time: 90d
```

## Health Check

The relay also exposes `/healthz` on the admin port:

```bash
curl http://localhost:9090/healthz
# {"status":"ok","agents":3,"streams":42}
```

Use this for load balancer health checks and uptime monitoring.
