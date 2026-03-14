# Monitoring and Observability

This document describes the metrics, dashboards, alerting rules, and logging configuration for operating atlax in production.

---

## Prometheus Metrics

The relay and agent both expose metrics in Prometheus exposition format at their configured metrics endpoint (default `:9090/metrics`).

### Relay Metrics

#### Gauges

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `atlax_relay_agents_connected` | gauge | | Number of currently connected agents. |
| `atlax_relay_streams_active` | gauge | | Number of currently active multiplexed streams across all agents. |

#### Counters

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `atlax_relay_streams_total` | counter | | Total number of streams opened since relay start. |
| `atlax_relay_bytes_transferred_total` | counter | `direction`, `customer_id` | Total bytes transferred. `direction` is `ingress` (client to agent) or `egress` (agent to client). |

#### Histograms

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `atlax_relay_handshake_duration_seconds` | histogram | | Time to complete mTLS handshake with an agent. Buckets: 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0. |
| `atlax_relay_stream_duration_seconds` | histogram | | Duration of a stream from open to close. Buckets: 0.1, 0.5, 1.0, 5.0, 10.0, 30.0, 60.0, 300.0, 600.0, 1800.0. |

### Agent Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `atlax_agent_connection_status` | gauge | | Current connection status. 1 = connected, 0 = disconnected. |
| `atlax_agent_reconnections_total` | counter | | Total number of reconnection attempts since agent start. |

### Custom Labels

The `customer_id` label is extracted from the agent's mTLS certificate subject (`CN=customer-{uuid}`). This enables per-customer dashboards, billing, and capacity planning.

---

## Grafana Dashboards

### Relay Overview Dashboard

A single-pane dashboard for relay health. Panels include:

**Row 1: Connection Health**

- Connected agents (current value, `atlax_relay_agents_connected`)
- Active streams (current value, `atlax_relay_streams_active`)
- Streams opened rate (`rate(atlax_relay_streams_total[5m])`)
- Reconnections rate across all agents (if agents expose metrics to a shared collector)

**Row 2: Throughput**

- Bytes transferred per second by direction (`rate(atlax_relay_bytes_transferred_total[5m])`)
- Top 10 customers by bytes transferred
- Total bandwidth utilization over time

**Row 3: Latency**

- Handshake duration p50/p95/p99 (`histogram_quantile(0.95, rate(atlax_relay_handshake_duration_seconds_bucket[5m]))`)
- Stream duration distribution
- Handshake duration heatmap

**Row 4: Resource Utilization**

- Go runtime metrics (goroutines, heap, GC pause)
- Process CPU and memory from node_exporter
- File descriptor usage

### Per-Customer Dashboard

Filtered by `customer_id` label variable:

- Bytes transferred (ingress/egress) for the selected customer
- Active streams for the selected customer
- Stream duration for the selected customer

### Agent Dashboard

For environments where agent metrics are collected centrally:

- Connection status timeline (`atlax_agent_connection_status`)
- Reconnection rate (`rate(atlax_agent_reconnections_total[5m])`)
- Time since last successful connection

---

## Alerting Rules

### Prometheus Alerting Rules

```yaml
# /etc/prometheus/rules/atlax.yml
groups:
  - name: atlax_relay
    rules:
      - alert: AtlaxRelayDown
        expr: up{job="atlax-relay"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "atlax relay is unreachable"
          description: "The Prometheus target for the atlax relay has been down for more than 1 minute."

      - alert: AtlaxRelayNoAgents
        expr: atlax_relay_agents_connected == 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "No agents connected to relay"
          description: "The relay has had zero connected agents for over 5 minutes."

      - alert: AtlaxRelayHighStreamCount
        expr: atlax_relay_streams_active > 80000
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High active stream count on relay"
          description: "Active streams exceed 80,000. Current value: {{ $value }}."

      - alert: AtlaxRelayHandshakeSlow
        expr: histogram_quantile(0.95, rate(atlax_relay_handshake_duration_seconds_bucket[5m])) > 1.0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Slow mTLS handshakes on relay"
          description: "p95 handshake duration exceeds 1 second. Current p95: {{ $value }}s."

      - alert: AtlaxRelayHandshakeVerySlow
        expr: histogram_quantile(0.99, rate(atlax_relay_handshake_duration_seconds_bucket[5m])) > 5.0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Very slow mTLS handshakes on relay"
          description: "p99 handshake duration exceeds 5 seconds. Possible certificate verification or resource issue."

      - alert: AtlaxAgentDisconnected
        expr: atlax_agent_connection_status == 0
        for: 3m
        labels:
          severity: warning
        annotations:
          summary: "Agent disconnected from relay"
          description: "An atlax agent has been disconnected for more than 3 minutes."

      - alert: AtlaxAgentHighReconnections
        expr: rate(atlax_agent_reconnections_total[15m]) > 0.1
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Agent reconnecting frequently"
          description: "Agent is reconnecting more than once every 10 minutes. Possible network instability."
```

### Recommended Notification Channels

| Severity | Channel | Response Time |
|----------|---------|---------------|
| critical | PagerDuty / Opsgenie | Immediate |
| warning | Slack / email | Within 1 hour |
| info | Dashboard only | Next business day |

---

## Logging

### Format

Both binaries use `log/slog` with JSON output by default. Every log entry includes structured fields for machine parsing and correlation.

### JSON Log Structure

```json
{
  "time": "2026-03-14T10:30:45.123Z",
  "level": "INFO",
  "msg": "agent connected",
  "component": "relay",
  "customer_id": "customer-a1b2c3d4",
  "remote_addr": "203.0.113.50:41234",
  "tls_version": "TLS 1.3",
  "stream_id": 0
}
```

### Standard Fields

| Field | Description |
|-------|-------------|
| `time` | ISO 8601 timestamp with millisecond precision |
| `level` | Log level: DEBUG, INFO, WARN, ERROR |
| `msg` | Human-readable message |
| `component` | Source component (relay, agent, protocol, auth) |
| `customer_id` | Customer identifier from mTLS certificate |
| `remote_addr` | Remote network address |
| `stream_id` | Stream identifier (0 for connection-level events) |
| `error` | Error message (present only on WARN/ERROR entries) |

### Log Levels

| Level | When to Use |
|-------|-------------|
| `DEBUG` | Frame-level details, state transitions, flow control window updates. High volume; disable in production unless debugging. |
| `INFO` | Connection established/closed, stream opened/closed, certificate loaded, configuration applied. |
| `WARN` | Reconnection attempt, certificate expiring soon, stream reset, rate limit triggered. |
| `ERROR` | TLS handshake failure, certificate verification failure, unrecoverable stream error, internal panic recovery. |

### Log Aggregation

Recommended stack for centralized logging:

1. **Collection:** Promtail, Fluentd, or Vector reading from journald or container stdout
2. **Storage:** Loki (lightweight) or Elasticsearch (full-text search)
3. **Visualization:** Grafana with Loki data source

### Correlation

To trace a client request end-to-end:

1. Client connects to relay on a service port. Relay logs the stream open with `stream_id` and `customer_id`.
2. Relay forwards the stream request to the agent. Agent logs the stream open with the same `stream_id`.
3. Agent dials the local service. Agent logs the local connection with `stream_id`.
4. On close, both sides log the stream close with duration and bytes transferred.

Filter logs by `customer_id` and `stream_id` to reconstruct the full path of any stream.
