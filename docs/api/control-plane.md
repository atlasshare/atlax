# Admin API

The relay exposes an HTTP API for health checks, Prometheus metrics, and runtime administration. This API runs on a separate listener from the agent TLS port.

---

## Transport

### TCP (default)

Address: configured via `admin_addr` in `relay.yaml` (e.g., `127.0.0.1:9090`).

No authentication on community edition. Bind to `127.0.0.1` to restrict to local access. Enterprise edition adds bearer token authentication for remote fleet management.

### Unix Domain Socket (opt-in)

Path: configured via `admin_socket` in `relay.yaml` (e.g., `/run/atlax/atlax.sock`). Empty by default (disabled).

Permissions: `0660` -- access controlled by filesystem permissions. No authentication required.

When using systemd, add `RuntimeDirectory=atlax` to the unit file so `/run/atlax/` is created with the correct ownership.

```bash
# Query health via socket
curl --unix-socket /run/atlax/atlax.sock http://localhost/healthz
```

If the socket path is configured but creation fails (e.g., permission denied), the admin server logs a warning and continues with TCP only. If socket-only mode is configured (no `admin_addr`), socket failure is fatal.

### Dual Mode

Both transports can run simultaneously. If both `admin_socket` and `admin_addr` are configured, the admin server listens on both. Socket failure in dual mode is non-fatal.

---

## Endpoints

### GET /healthz

Liveness probe. Returns agent and stream counts.

**Response:**

```json
{
  "status": "ok",
  "agents": 2,
  "streams": 15
}
```

---

### GET /readyz

Readiness probe. Returns 200 when the registry is reachable and the admin server is serving requests. Suitable for ALB target group health checks. Distinct from `/healthz` which additionally reports agent/stream counts.

**Response (ready):** `200 OK`

```json
{"status": "ready"}
```

**Response (not ready):** `503 Service Unavailable`

```json
{"status": "not ready"}
```

---

### GET /metrics

Prometheus exposition format. Serves all `atlax_*` metrics via `promhttp.Handler()`.

See `docs/operations/prometheus.md` for the full metrics reference.

---

### GET /stats

Relay uptime and current connection state.

**Response:**

```json
{
  "status": "ok",
  "uptime": "2h30m15s",
  "uptime_seconds": 9015.0,
  "agents": 2,
  "streams": 15
}
```

---

### GET /ports

List all active port-to-customer mappings.

**Response:**

```json
[
  {
    "port": 18445,
    "customer_id": "customer-001",
    "service": "smb",
    "listen_addr": "127.0.0.1",
    "max_streams": 0
  },
  {
    "port": 18080,
    "customer_id": "customer-001",
    "service": "http",
    "listen_addr": "0.0.0.0",
    "max_streams": 50
  }
]
```

---

### GET /ports/{port}

Inspect a single port mapping.

**Success:** `200 OK` with the same `PortResponse` object as `GET /ports`.

**Errors:**
- `400 Bad Request` -- invalid port number
- `404 Not Found` -- no mapping for this port

---

### POST /ports

Add a port mapping at runtime and start a TCP listener on that port.

**Request:**

```json
{
  "port": 19090,
  "customer_id": "customer-002",
  "service": "api",
  "max_streams": 100,
  "listen_addr": "0.0.0.0"
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `port` | int | yes | -- | TCP port to listen on |
| `customer_id` | string | yes | -- | Customer to route traffic to |
| `service` | string | yes | -- | Service name sent to agent in STREAM_OPEN payload |
| `max_streams` | int | no | 0 (unlimited) | Maximum concurrent streams for this port |
| `listen_addr` | string | no | `0.0.0.0` | Bind address for the TCP listener |

**Success:** `201 Created` with the created mapping.

**Errors:**
- `400 Bad Request` -- missing required fields or invalid JSON
- `409 Conflict` -- port already in use (address already bound)

**Behavior:** Creates the routing entry AND starts a TCP listener. If the listener fails to bind (port in use, permission denied), the routing entry is rolled back and `409` is returned.

**Persistence:** Runtime-only. Changes are lost on restart. The config file is the source of truth.

---

### DELETE /ports/{port}

Remove a port mapping and stop the TCP listener.

**Success:** `204 No Content`

**Errors:**
- `400 Bad Request` -- invalid port number
- `404 Not Found` -- no mapping for this port
- `405 Method Not Allowed` -- non-DELETE method

**Behavior:** Removes the routing entry AND stops the TCP listener. If the listener was started from config (not the admin API), the stop is a no-op and a warning is logged.

---

### GET /agents

List all connected agents.

**Response:**

```json
[
  {
    "customer_id": "customer-001",
    "remote_addr": "203.0.113.50:41234",
    "connected_at": "2026-03-14T08:15:30Z",
    "last_seen": "2026-03-14T10:30:45Z",
    "stream_count": 12
  }
]
```

---

### GET /agents/{customerID}

Inspect a single connected agent.

**Success:** `200 OK` with the same `AgentResponse` object as `GET /agents`.

**Errors:**
- `400 Bad Request` -- empty customer ID
- `404 Not Found` -- agent not connected

---

### DELETE /agents/{customerID}

Force-disconnect an agent. Calls `registry.Unregister` which closes the mux session. The agent will reconnect automatically via its supervision loop.

**Success:** `204 No Content`

**Errors:**
- `400 Bad Request` -- empty customer ID
- `404 Not Found` -- agent not connected
- `405 Method Not Allowed` -- non-DELETE method

---

## Error Format

All error responses are JSON:

```json
{
  "error": "descriptive error message"
}
```

HTTP status codes are always set correctly (200, 201, 204, 400, 404, 405, 409, 500).

---

## Enterprise Extensions

The enterprise edition extends the admin API with:

- **TCP + bearer token authentication** for remote fleet management
- **Multi-relay fleet endpoints** via distributed registry
- **Dynamic port allocation** via control plane API
- **RBAC** for admin operations

The community admin API remains fully functional for single-relay deployments.

---

## Integration with ats CLI

The `ats` CLI tool (in the `atlax-tools` repo) detects the unix socket at the configured path and uses it for admin operations. When the socket is not available, `ats` falls back to the TCP admin address or direct config file editing.
