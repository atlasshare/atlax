# Operational Runbooks

This document provides step-by-step procedures for diagnosing and resolving common operational issues with atlax. Each runbook follows a consistent structure: symptoms, diagnosis, resolution, and prevention.

---

## Runbook 1: Agent Not Connecting

### Symptoms

- `atlax_relay_agents_connected` gauge does not increase after agent deployment
- `AtlaxAgentDisconnected` alert fires
- Agent logs show repeated `ERROR` entries with TLS or connection errors
- Relay logs show no handshake attempts from the expected customer ID

### Diagnosis

1. **Check agent logs for connection errors:**
   ```bash
   journalctl -u atlax-agent --since "10 minutes ago" | grep -i error
   ```

2. **Verify network connectivity from agent to relay:**
   ```bash
   # Test TCP connectivity
   nc -zv relay.example.com 8443

   # Test TLS handshake
   openssl s_client -connect relay.example.com:8443 \
     -cert /etc/atlax/agent.crt \
     -key /etc/atlax/agent.key \
     -CAfile /etc/atlax/relay-ca.crt
   ```

3. **Check certificate validity:**
   ```bash
   openssl x509 -in /etc/atlax/agent.crt -noout -dates -subject
   openssl verify -CAfile /etc/atlax/relay-ca.crt /etc/atlax/agent.crt
   ```

4. **Verify relay is accepting connections:**
   ```bash
   curl -s http://relay.example.com:8080/readyz
   ```

5. **Check firewall rules on both sides:**
   ```bash
   # On agent node: verify outbound to relay port
   iptables -L OUTPUT -n | grep 8443

   # On relay: verify inbound on agent port
   iptables -L INPUT -n | grep 8443
   ```

### Resolution

| Cause | Fix |
|-------|-----|
| Certificate expired | Renew certificate (see [Certificate Operations](certificate-ops.md)) |
| Certificate not signed by expected CA | Verify CA chain matches relay configuration |
| DNS resolution failure | Check `/etc/resolv.conf`, test with `dig relay.example.com` |
| Firewall blocking outbound | Allow TCP outbound to relay port |
| Relay not running | Start relay service: `systemctl start atlax-relay` |
| Relay at max agent capacity | Increase `ATLAX_RELAY_MAX_AGENTS` or deploy additional relay |

### Prevention

- Monitor certificate expiry with Prometheus (`x509_cert_not_after` from blackbox exporter)
- Automate certificate renewal at least 30 days before expiry
- Set up `AtlaxAgentDisconnected` alerts with 3-minute threshold
- Test connectivity during initial deployment with the verification steps above

---

## Runbook 2: High Stream Count

### Symptoms

- `AtlaxRelayHighStreamCount` alert fires
- `atlax_relay_streams_active` gauge is abnormally high (exceeding baseline by 2x or more)
- Relay memory usage is increasing
- Client connections may become slow or time out

### Diagnosis

1. **Check current stream count and rate:**
   ```bash
   curl -s http://localhost:9090/metrics | grep atlax_relay_streams
   ```

2. **Identify which customers have the most streams:**
   ```bash
   # Query Prometheus for top customers by active byte transfer rate
   # In Grafana or PromQL:
   # topk(10, rate(atlax_relay_bytes_transferred_total[5m]))
   ```

3. **Check for stream leaks (streams that never close):**
   ```bash
   # Look for long-duration streams in logs
   journalctl -u atlax-relay --since "1 hour ago" | grep "stream_duration"
   ```

4. **Check relay resource usage:**
   ```bash
   # Goroutine count (indicates stream count roughly)
   curl -s http://localhost:9090/metrics | grep go_goroutines

   # Memory usage
   curl -s http://localhost:9090/metrics | grep process_resident_memory_bytes
   ```

### Resolution

| Cause | Fix |
|-------|-----|
| Legitimate traffic spike | Scale relay resources or add relay instances |
| Client application opening too many connections | Coordinate with customer to reduce concurrency |
| Stream leak (streams not closing) | Identify bug, apply fix, restart relay with GOAWAY (see Runbook 4) |
| Attack or abuse | Apply per-customer rate limits, disconnect offending agent if necessary |

### Prevention

- Set per-customer stream limits via `ATLAX_RELAY_MAX_STREAMS_PER_AGENT`
- Configure idle stream timeout to close stale streams automatically
- Monitor `atlax_relay_streams_active` with a warning threshold at 80% of capacity
- Establish baseline stream count per customer for anomaly detection

---

## Runbook 3: Certificate Expiry Rotation

### Symptoms

- Certificate expiry monitoring alert fires (less than 30 days remaining)
- Agent logs show `WARN` entries about certificate expiring soon
- TLS handshake failures begin appearing as certificates expire

### Diagnosis

1. **Check certificate expiry dates:**
   ```bash
   # Relay certificate
   openssl x509 -in /etc/atlax/relay.crt -noout -enddate

   # Agent certificate (on agent node)
   openssl x509 -in /etc/atlax/agent.crt -noout -enddate

   # Intermediate CA certificates
   openssl x509 -in /etc/atlax/customer-ca.crt -noout -enddate
   ```

2. **Verify the full certificate chain:**
   ```bash
   openssl verify -show_chain \
     -CAfile /etc/atlax/root-ca.crt \
     -untrusted /etc/atlax/intermediate-ca.crt \
     /etc/atlax/relay.crt
   ```

### Resolution

1. Generate a new CSR or certificate using the appropriate CA (see [Certificate Operations](certificate-ops.md)).
2. Deploy the new certificate to the target host.
3. For the relay, trigger a certificate hot-reload (the relay watches the cert file for changes) or restart with GOAWAY for a graceful rotation.
4. For the agent, the agent checks certificate expiry daily and will automatically submit a renewal CSR when less than 30 days remain.
5. Verify the new certificate is in use:
   ```bash
   openssl s_client -connect relay.example.com:8443 </dev/null 2>/dev/null \
     | openssl x509 -noout -dates
   ```

### Prevention

- Automate certificate renewal through the control plane API
- Monitor certificate expiry with alerting at 30, 14, and 7 days before expiry
- Maintain an inventory of all issued certificates (see [Certificate Operations](certificate-ops.md))
- Test certificate rotation in staging before production

---

## Runbook 4: Relay Restart with GOAWAY

### Symptoms

- Relay needs to be restarted for upgrade, configuration change, or issue resolution
- Active agents and streams must be handled gracefully

### Diagnosis

1. **Assess current load before restart:**
   ```bash
   curl -s http://localhost:9090/metrics | grep -E "agents_connected|streams_active"
   ```

2. **Determine if a graceful restart is possible or if immediate restart is required:**
   - Graceful: planned maintenance, upgrade, configuration change
   - Immediate: security incident, unrecoverable crash loop

### Resolution

**Graceful restart (preferred):**

1. Send SIGTERM to the relay process. The relay will:
   - Stop accepting new agent connections
   - Send GOAWAY frames to all connected agents
   - Wait for active streams to complete (up to a configurable drain timeout)
   - Close all connections
   - Exit cleanly

   ```bash
   # Systemd will send SIGTERM
   systemctl stop atlax-relay

   # Or manually
   kill -TERM $(pidof atlax-relay)
   ```

2. Agents receiving GOAWAY will:
   - Stop opening new streams on the current connection
   - Complete existing streams
   - Reconnect to the relay (or a different relay in active-active) after the connection closes

3. Start the new relay version:
   ```bash
   systemctl start atlax-relay
   ```

4. Verify agents have reconnected:
   ```bash
   curl -s http://localhost:9090/metrics | grep atlax_relay_agents_connected
   ```

**Immediate restart (emergency only):**

```bash
systemctl restart atlax-relay
```

Agents will detect the connection drop and begin reconnecting with exponential backoff.

### Prevention

- Schedule maintenance windows during low-traffic periods
- In active-active deployments, drain one relay at a time
- Test GOAWAY behavior in staging with representative agent and stream counts

---

## Runbook 5: Tenant Isolation Breach

### Symptoms

- A customer reports receiving data that does not belong to them
- Audit logs show stream routing to an incorrect customer ID
- Security monitoring detects cross-customer stream routing

### Diagnosis

**This is a critical security incident. Follow the incident response process immediately.**

1. **Preserve evidence:**
   ```bash
   # Capture current relay state
   curl -s http://localhost:8080/api/v1/agents > /tmp/incident-agents-$(date +%s).json
   curl -s http://localhost:9090/metrics > /tmp/incident-metrics-$(date +%s).txt

   # Capture relay logs
   journalctl -u atlax-relay --since "1 hour ago" > /tmp/incident-logs-$(date +%s).txt
   ```

2. **Identify the affected streams:**
   - Search logs for the reported customer ID
   - Cross-reference stream IDs with connection records
   - Verify the customer ID in the mTLS certificate matches the stream routing

3. **Check for configuration errors:**
   - Verify customer port allocation does not overlap
   - Verify the agent registry maps each customer ID to exactly one connection
   - Check for race conditions in agent registration/deregistration

### Resolution

1. **Immediately disconnect the affected agents:**
   ```bash
   # Via control plane API
   curl -X POST http://localhost:8080/api/v1/agents/{customer_id}/disconnect
   ```

2. **If the root cause is a software bug:**
   - Roll back to the last known good version
   - Do not restore service until the bug is identified and a fix is confirmed

3. **Notify affected customers** through the established incident communication channel.

4. **Conduct a post-incident review** within 48 hours.

### Prevention

- Enforce customer ID validation on every stream open, not just at connection time
- Implement stream-level audit logging for forensic analysis
- Run isolation integration tests as part of CI (verify streams cannot cross customer boundaries)
- Conduct periodic security audits of the routing logic

---

## Runbook 6: Performance Degradation

### Symptoms

- Client-perceived latency increases (reported by customers or detected by synthetic monitoring)
- `atlax_relay_handshake_duration_seconds` p95 increasing
- `atlax_relay_stream_duration_seconds` distribution shifting higher
- Relay CPU or memory usage spiking
- Throughput decreasing despite constant stream count

### Diagnosis

1. **Check relay resource utilization:**
   ```bash
   # CPU and memory
   top -p $(pidof atlax-relay)

   # Goroutine count (should correlate with stream count)
   curl -s http://localhost:9090/metrics | grep go_goroutines

   # GC pressure
   curl -s http://localhost:9090/metrics | grep go_gc_duration_seconds
   ```

2. **Check network health:**
   ```bash
   # Network interface errors and drops
   ip -s link show eth0

   # TCP connection states
   ss -s

   # Retransmission rate
   ss -ti | grep retrans
   ```

3. **Check for flow control bottlenecks:**
   ```bash
   # Look for WINDOW_UPDATE stalls in debug logs
   journalctl -u atlax-relay --since "5 minutes ago" | grep -i "window"
   ```

4. **Check host-level resource limits:**
   ```bash
   # File descriptor usage
   ls /proc/$(pidof atlax-relay)/fd | wc -l
   cat /proc/$(pidof atlax-relay)/limits | grep "open files"

   # Check for OOM kills
   dmesg | grep -i "out of memory"
   ```

### Resolution

| Cause | Fix |
|-------|-----|
| CPU saturation | Scale vertically (larger instance) or horizontally (additional relay) |
| Memory exhaustion | Check for goroutine/buffer leaks, increase memory, tune `GOGC` |
| File descriptor limit | Increase `LimitNOFILE` in systemd unit, verify with `ulimit -n` |
| Network interface saturation | Upgrade network bandwidth or distribute load across relays |
| GC pressure | Tune `GOGC` environment variable (higher = less frequent GC, more memory) |
| Flow control stalls | Increase default window sizes, investigate slow agents |
| Slow agents | Identify agents with high stream duration, check their local service health |

### Prevention

- Establish performance baselines during initial deployment
- Set resource utilization alerts at 70% capacity
- Load test before production deployment (target: 1000 agents, 100 streams per agent)
- Monitor Go runtime metrics (goroutines, heap, GC) alongside application metrics
- Plan capacity increases before hitting limits
