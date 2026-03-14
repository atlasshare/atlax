# Control Plane API

The relay exposes an HTTP API for health checks, metrics, and administrative operations. This API runs on a separate listener from the agent TLS port (default `:8080`) and is authenticated via mTLS with an admin certificate.

---

## Endpoints

### GET /healthz

Liveness probe. Returns 200 if the relay process is running. Does not check downstream dependencies.

**Authentication:** None (unauthenticated for load balancer probes).

**Response:**

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "success": true,
  "data": {
    "status": "ok"
  },
  "error": null
}
```

---

### GET /readyz

Readiness probe. Returns 200 if the relay is ready to accept agent connections and serve client traffic. Returns 503 during startup, certificate reload, or graceful shutdown.

**Authentication:** None (unauthenticated for load balancer probes).

**Response (ready):**

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "success": true,
  "data": {
    "status": "ready",
    "checks": {
      "tls_listener": "ok",
      "certificates_loaded": "ok",
      "registry_initialized": "ok"
    }
  },
  "error": null
}
```

**Response (not ready):**

```http
HTTP/1.1 503 Service Unavailable
Content-Type: application/json

{
  "success": false,
  "data": null,
  "error": "relay is not ready: certificates are being reloaded"
}
```

---

### GET /metrics

Prometheus metrics in exposition format. See [Monitoring and Observability](../operations/monitoring.md) for the full list of exported metrics.

**Authentication:** None (restrict access via firewall rules; see [Deployment Guide](../operations/deployment.md)).

**Response:**

```http
HTTP/1.1 200 OK
Content-Type: text/plain; version=0.0.4; charset=utf-8

# HELP atlax_relay_agents_connected Number of currently connected agents.
# TYPE atlax_relay_agents_connected gauge
atlax_relay_agents_connected 42
# HELP atlax_relay_streams_active Number of currently active streams.
# TYPE atlax_relay_streams_active gauge
atlax_relay_streams_active 1583
...
```

---

### GET /api/v1/agents

List all currently connected agents with their metadata.

**Authentication:** mTLS with an admin certificate. The admin certificate must be signed by a trusted admin CA configured on the relay.

**Rate Limiting:** 10 requests per second per admin certificate.

**Response:**

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "success": true,
  "data": {
    "agents": [
      {
        "customer_id": "customer-a1b2c3d4",
        "relay_id": "relay-01",
        "remote_addr": "203.0.113.50:41234",
        "connected_at": "2026-03-14T08:15:30Z",
        "last_seen": "2026-03-14T10:30:45Z",
        "cert_serial": "1A2B3C4D5E6F",
        "cert_expiry": "2026-06-12T08:15:30Z",
        "assigned_ports": [10001, 10002, 10003],
        "active_streams": 12
      },
      {
        "customer_id": "customer-e5f6g7h8",
        "relay_id": "relay-01",
        "remote_addr": "198.51.100.22:55678",
        "connected_at": "2026-03-14T09:00:00Z",
        "last_seen": "2026-03-14T10:30:42Z",
        "cert_serial": "7F8E9D0C1B2A",
        "cert_expiry": "2026-06-10T09:00:00Z",
        "assigned_ports": [10011, 10012],
        "active_streams": 3
      }
    ],
    "metadata": {
      "total": 2
    }
  },
  "error": null
}
```

**Error Response (authentication failure):**

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{
  "success": false,
  "data": null,
  "error": "client certificate required"
}
```

---

### GET /api/v1/agents/{customer_id}

Retrieve details for a specific agent by customer ID.

**Authentication:** mTLS with an admin certificate.

**Rate Limiting:** 10 requests per second per admin certificate.

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `customer_id` | string | The customer identifier (e.g., `customer-a1b2c3d4`) |

**Response (found):**

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "success": true,
  "data": {
    "customer_id": "customer-a1b2c3d4",
    "relay_id": "relay-01",
    "remote_addr": "203.0.113.50:41234",
    "connected_at": "2026-03-14T08:15:30Z",
    "last_seen": "2026-03-14T10:30:45Z",
    "cert_serial": "1A2B3C4D5E6F",
    "cert_expiry": "2026-06-12T08:15:30Z",
    "assigned_ports": [10001, 10002, 10003],
    "active_streams": 12
  },
  "error": null
}
```

**Response (not found):**

```http
HTTP/1.1 404 Not Found
Content-Type: application/json

{
  "success": false,
  "data": null,
  "error": "agent not found: customer-a1b2c3d4"
}
```

---

### POST /api/v1/agents/{customer_id}/disconnect

Force-disconnect an agent. The relay sends a GOAWAY frame to the agent and closes the connection. The agent will reconnect automatically.

**Authentication:** mTLS with an admin certificate.

**Rate Limiting:** 5 requests per second per admin certificate.

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `customer_id` | string | The customer identifier to disconnect |

**Response (success):**

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "success": true,
  "data": {
    "customer_id": "customer-a1b2c3d4",
    "action": "disconnected",
    "active_streams_closed": 12
  },
  "error": null
}
```

**Response (not found):**

```http
HTTP/1.1 404 Not Found
Content-Type: application/json

{
  "success": false,
  "data": null,
  "error": "agent not found: customer-a1b2c3d4"
}
```

---

## Authentication

### mTLS Admin Certificates

Administrative API endpoints (`/api/v1/*`) require mTLS authentication. The relay is configured with a trusted admin CA, separate from the customer CA used for agent connections.

```yaml
# relay.yaml
control_plane:
  listen_addr: ":8080"
  admin_ca_file: "/etc/atlax/admin-ca.crt"
```

Admin certificates must:
- Be signed by the configured admin CA.
- Have `extendedKeyUsage = clientAuth`.
- Have a `CN` that identifies the admin operator (for audit logging).

### Unauthenticated Endpoints

The following endpoints do not require authentication and should be protected by firewall rules (restrict to internal monitoring networks):

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`

---

## Rate Limiting

All authenticated API endpoints are rate-limited per admin certificate identity (extracted from the certificate CN).

| Endpoint | Rate Limit |
|----------|-----------|
| `GET /api/v1/agents` | 10 req/s |
| `GET /api/v1/agents/{customer_id}` | 10 req/s |
| `POST /api/v1/agents/{customer_id}/disconnect` | 5 req/s |

Exceeding the rate limit returns:

```http
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
Retry-After: 1

{
  "success": false,
  "data": null,
  "error": "rate limit exceeded, retry after 1 second"
}
```

---

## Response Format

All API responses follow a consistent JSON envelope:

```go
type APIResponse struct {
    // Success indicates whether the request was processed without error.
    Success bool `json:"success"`

    // Data contains the response payload. Null on error.
    Data any `json:"data"`

    // Error contains the error message. Null on success.
    Error *string `json:"error"`
}
```

### Design Rationale

- The `success` field provides a quick boolean check without inspecting the HTTP status code.
- The `data` field is polymorphic (any type) to support different response structures per endpoint.
- The `error` field is a pointer to distinguish between null (no error) and an empty string.
- HTTP status codes are always set correctly (200, 401, 404, 429, 503) in addition to the envelope.
