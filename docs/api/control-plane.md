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
    "stream_count": 12,
    "services": ["smb", "http"],
    "cert_not_after": "2026-07-15T00:00:00Z"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `services` | `[]string` | Service names this agent forwards locally. Populated from the `CmdServiceList` (0x0E) frame the agent sends once after mTLS handshake. Empty array if the agent did not emit the frame within 50ms or is an older version. |
| `cert_not_after` | string (RFC3339) | Agent's mTLS client certificate expiry. Zero value (`"0001-01-01T00:00:00Z"`) if not yet captured. |

---

### GET /agents/{customerID}

Inspect a single connected agent.

**Success:** `200 OK` with the same `AgentResponse` object as `GET /agents` (including `services` and `cert_not_after`).

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

### GET /config

Return the currently-loaded runtime configuration. This reflects the result of the most recent successful parse -- either the initial load at startup or the last successful `POST /reload`.

**Response:**

```json
{
  "server": {
    "listen_addr": "0.0.0.0:8443",
    "admin_addr": "127.0.0.1:9090",
    "admin_socket": "/run/atlax/atlax.sock",
    "store_path": "/var/lib/atlax/sidecar.json",
    "shutdown_grace_period": "30s"
  },
  "tls": {
    "cert_file": "/etc/atlax/relay.crt",
    "key_file": "/etc/atlax/relay.key",
    "client_ca_file": "/etc/atlax/customer-ca.crt"
  },
  "customers": [ ... ],
  "ports": [ ... ],
  "metrics": { ... }
}
```

Full schema reference: see `configs/relay.example.yaml` or `docs/operations/production-setup.md`.

**No redaction:** `RelayConfig` contains paths and operational data only -- no secret material (passwords, tokens, or private key content). Verified in the Step 5 step report.

**Defensive copy:** Each response reflects a point-in-time snapshot. A concurrent reload does not partially leak through.

**Errors:**
- `405 Method Not Allowed` -- non-GET method

---

### POST /reload

Trigger a hot-reload of `relay.yaml` without restarting the relay process. Also invokable via `SIGHUP` signal to the relay process (wired in `cmd/relay/main.go`).

**Request body:** none

**Behavior:**

1. Re-reads the config file from the path passed at startup
2. Validates via the normal `config.LoadRelayConfig` path
3. Diffs against current runtime state:
   - New ports (in new config, not in current): `AddPortMapping` + start listener
   - Removed ports (in current, not in new): `RemovePortMapping` + stop listener
   - Changed ports (same port, different mutable fields): `UpdatePortMapping`
   - Changed rate limits: apply per-customer rate limiter updates
4. **Security invariant:** rejects any port whose `customer_id` differs between old and new config. Rejected ports are left at their original binding. Rejection is per-port, not all-or-nothing -- other non-conflicting changes in the same reload succeed.
5. Fields that require a process restart (TLS cert/key paths, server listen addr, agent listen addr) are logged as warnings and listed in the `restart_required` response field. These changes are NOT applied at runtime.

**Success:** `200 OK`

```json
{
  "ports_added": 1,
  "ports_removed": 0,
  "ports_updated": 2,
  "ports_rejected": 0,
  "rate_limits_changed": 1,
  "restart_required": ["tls.cert_file"]
}
```

**Errors:**
- `405 Method Not Allowed` -- non-POST method
- `422 Unprocessable Entity` -- config parse or validation error. The runtime state is unchanged.

**Serialization:** Reloads are serialized (one at a time). A `SIGHUP` arriving during an in-flight reload is buffered (1-deep channel) and executed after the current reload completes.

**Audit event:** Emits `admin.reload` with the summary as metadata.

**SIGHUP:** `cmd/relay/main.go` wires `syscall.SIGHUP` to call the same `Reload()` method. `SIGINT`/`SIGTERM` remain shutdown signals.

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
