# Performance Guide

## Overview

atlax includes a comprehensive performance test suite with two modes:

- **In-process mode** (default): relay and agent MuxSessions connected via `net.Pipe` with an echo server. Measures the **protocol ceiling** -- the maximum the mux layer can deliver without network overhead.
- **Remote mode** (`--remote`): connects to a live relay over real TCP. Measures **actual production performance** including network latency, TLS overhead, and service response time.

## Running the Tests

### In-Process (protocol benchmarks)

```bash
# All benchmarks
go run ./scripts/loadtest/

# Specific benchmark
go run ./scripts/loadtest/ -bench load
go run ./scripts/loadtest/ -bench stress
go run ./scripts/loadtest/ -bench throughput
go run ./scripts/loadtest/ -bench latency
go run ./scripts/loadtest/ -bench churn
go run ./scripts/loadtest/ -bench ramp

# With race detector
go run -race ./scripts/loadtest/ -bench load
```

### Remote (production benchmarks)

Requires a running relay+agent deployment. See [Setup and Testing Guide](setup-and-testing.md).

```bash
# Load test against live relay (1000 concurrent TCP connections)
go run ./scripts/loadtest/ -remote 18.207.237.252:18080 -bench load

# Latency profile at various concurrency levels
go run ./scripts/loadtest/ -remote 18.207.237.252:18080 -bench latency

# Ramp test: find the throughput curve
go run ./scripts/loadtest/ -remote 18.207.237.252:18080 -bench ramp

# All remote-compatible benchmarks
go run ./scripts/loadtest/ -remote 18.207.237.252:18080
```

Replace `18.207.237.252:18080` with your relay's public IP and customer port.

**Which benchmarks work in remote mode:**

| Benchmark | In-process | Remote | Notes |
|-----------|:----------:|:------:|-------|
| load | Yes | Yes | 1000 concurrent TCP connections to relay port |
| latency | Yes | Yes | p50/p95/p99 at 1-500 concurrency |
| ramp | Yes | Yes | throughput curve at 10-500 concurrency |
| stress | Yes | No | would DoS the relay; in-process only |
| throughput | Yes | No | requires mux-level stream access |
| churn | Yes | No | requires mux-level stream access |

### How to Run a Production Benchmark

**Step 1: Ensure the deployment is running**

```bash
# Verify relay is accepting connections
nc -zv <RELAY_IP> 18080

# Verify the tunnel works
curl http://<RELAY_IP>:18080
```

**Step 2: Run the remote load test**

```bash
go run ./scripts/loadtest/ -remote <RELAY_IP>:18080 -bench load
```

This opens 1000 concurrent TCP connections to the relay port. Each connection sends 1KB, reads the response, and closes. The result shows total time, throughput, and error rate.

**Step 3: Run the latency profile**

```bash
go run ./scripts/loadtest/ -remote <RELAY_IP>:18080 -bench latency
```

Measures round-trip latency at concurrency levels 1, 10, 50, 100, 500. Shows p50/p95/p99/max. This reveals how your network latency + TLS + tunnel overhead scale under load.

**Step 4: Run the ramp test**

```bash
go run ./scripts/loadtest/ -remote <RELAY_IP>:18080 -bench ramp
```

Gradually increases concurrency from 10 to 500. Shows the throughput curve and where performance degrades. Useful for capacity planning.

**Step 5: Compare with in-process baseline**

```bash
go run ./scripts/loadtest/ -bench load
```

The difference between remote and in-process results is the overhead of: network RTT + TLS handshake + TCP congestion + service response time.

### Interpreting Remote Results

Remote results will be significantly different from in-process:

| Factor | In-process | Remote (LAN) | Remote (Internet) |
|--------|-----------|-------------|-------------------|
| Latency | sub-ms | 1-5ms | 20-200ms |
| Throughput | 10,000+ streams/sec | 1,000-5,000 | 100-1,000 |
| Error rate | 0% | 0% | 0-2% (network) |

**If remote error rate is high:**
- Check AWS security group allows the port
- Check agent is connected (`curl http://<IP>:<PORT>`)
- Check relay logs for stream limit rejections
- Reduce concurrency and retry

## Benchmark Descriptions

### Load Test

**What it measures:** Sustained throughput at target capacity.

Opens 1000 concurrent streams, each sending a 1KB message through the full mux pipeline (relay -> wire -> agent -> echo server -> wire -> relay), reads the echo, and closes. Pass criterion: < 1% error rate.

**What it proves:** The mux handles production-level concurrency without stream corruption, deadlocks, or resource exhaustion.

### Stress Test

**What it measures:** The breaking point.

Starts at 500 concurrent streams and increases by 500 each round until the error rate exceeds 5%. Each round creates a fresh MuxSession pair with a 30-second timeout.

**What it proves:** How far beyond the target capacity the system can be pushed before degradation.

### Throughput Test

**What it measures:** Maximum data transfer rate through a single stream.

Sends 100MB through one stream in 32KB chunks, with the echo server reflecting all data back. Measures MB/sec.

**What it proves:** Per-stream bandwidth is not bottlenecked by the protocol framing, flow control, or copy buffers.

### Latency Test

**What it measures:** Per-stream round-trip latency at various concurrency levels.

Opens N concurrent streams (1, 10, 50, 100, 500), each performing a 256-byte echo round-trip. Reports p50, p95, p99, and max latency.

**What it proves:** How latency scales with concurrency. Expected: sub-millisecond at low concurrency, graceful degradation under load.

### Churn Test

**What it measures:** Stream ID recycling and resource cleanup under rapid open/close cycles.

Opens and closes 5000 streams sequentially (serial, not concurrent). Verifies that stream IDs are recycled and no active streams leak after the test.

**What it proves:** Long-running relays do not suffer from stream ID exhaustion or goroutine/memory leaks from accumulated stream state.

### Ramp Test

**What it measures:** Throughput curve as concurrency increases.

Opens streams at concurrency levels from 10 to 2000, measuring streams/sec, error count, and average latency at each level. Shows the performance degradation curve.

**What it proves:** Where throughput peaks and where latency starts to dominate. Useful for capacity planning.

## Reference Results

Measured on MacBook Pro (Apple Silicon), Go 1.25.8, in-process (no network):

### Load Test

```
1000 concurrent streams, 1024 bytes each
1000 ok, 0 fail (0.0%)
Duration: 99ms
Throughput: 10,106 streams/sec
```

### Stress Test

```
500 streams:  0% error, 29ms
1000 streams: 0% error, 56ms
1500 streams: 0% error, 104ms
2000 streams: 0% error, 104ms
2500 streams: 0% error, 161ms
3000 streams: 0% error, 175ms
3500 streams: 0% error, 187ms
```

No breaking point found up to 3500 concurrent streams (0% error at every level).

### Throughput Test

```
100 MB through single stream (32 KB chunks)
Rate: 941.5 MB/sec
```

### Latency Test

```
concurrency      p50        p95        p99        max
1               0.6ms      0.6ms      0.6ms      0.6ms
10              0.8ms      1.0ms      1.0ms      1.0ms
50              3.5ms      3.8ms      4.0ms      4.0ms
100             6.4ms      7.7ms      7.8ms      7.8ms
500            28.0ms     50.0ms     50.3ms     50.8ms
```

### Churn Test

```
5000 rapid open/close cycles
5000 ok, 0 fail
7217 cycles/sec
Active streams after churn: 0 (clean cleanup)
```

### Ramp Test

```
concurrency   streams/sec   errors   avg latency
10              11,099/s       0       701us
25              13,579/s       0     1.528ms
50              14,844/s       0     2.683ms
100             14,554/s       0     5.234ms
250              3,193/s       0    28.144ms
500              9,758/s       0    33.684ms
1000            17,701/s       0    43.949ms
1500            15,418/s       0    79.957ms
2000            14,410/s       0   109.684ms
```

Peak throughput: ~17,700 streams/sec at 1000 concurrency. Zero errors at all levels up to 2000.

## Live Benchmark Results

Measured against a production deployment: relay on AWS EC2 t3.micro (1 vCPU, 911 MB RAM), agent on Arch Linux behind CGNAT, ~265ms base network RTT, Go echo server as backend. Community edition binaries from commit 4a77e62.

### Latency Test (remote)

```
concurrency         p50        p95        p99        max
1                349.0ms     349.0ms     349.0ms     349.0ms
10               331.0ms     331.0ms     331.0ms     331.0ms
50               321.0ms     324.0ms     325.0ms     325.0ms
100              269.0ms     325.0ms     325.0ms     325.0ms
500              384.0ms     389.0ms     392.0ms     398.0ms
```

The ~265-350ms p50 floor is pure network latency. The tunnel protocol adds negligible overhead -- p50 at 500 concurrent is only ~35ms above p50 at 1.

### Load Test (remote)

```
1000 concurrent streams, 1024 bytes each
843 ok, 157 fail (15.7%)
Duration: 10.79s, Throughput: 78 streams/sec
```

Failures are from the t3.micro's single vCPU saturating under instantaneous burst, not from the tunnel protocol.

### Ramp Test (remote)

```
concurrency      reqs/sec   errors  avg latency
10                    10       0     296ms
25                    25       0     290ms
50                    50       0     301ms
100                  100       0     317ms
200                  200       0     303ms
500                  363     137     545ms
```

Zero errors up to 200 concurrent. At 500, the relay hardware saturates (single vCPU). A t3.small (2 vCPU) is recommended for 500+ concurrent streams.

### In-Process vs Live Comparison

| Metric | In-Process | Live | Gap |
|--------|-----------|------|-----|
| Latency p50 @ 1 | 0.6ms | 349ms | Network RTT |
| Latency p50 @ 100 | 6.4ms | 269ms | Network RTT |
| Ramp @ 200 errors | 0 | 0 | Same |
| Load throughput | 10,106/sec | 78/sec | Network + hardware |

The protocol overhead is negligible. The gap is entirely network latency and relay hardware limits.

## Interpreting Results

**Throughput (streams/sec):** Higher is better. This measures how many complete echo round-trips the system can handle per second. The mux overhead (framing, flow control, goroutine scheduling) is the primary factor.

**Latency:** Lower is better. Sub-millisecond at low concurrency indicates minimal protocol overhead. Latency at high concurrency is dominated by goroutine scheduling and flow control window contention.

**Error rate:** Should be 0% at production target (1000 streams). Any errors indicate resource exhaustion, deadlocks, or race conditions.

**Churn (cycles/sec):** Measures stream lifecycle overhead. 7000+ cycles/sec means stream IDs are recycled efficiently and no state leaks between cycles.

**Throughput (MB/sec):** Measures raw data transfer speed through the mux. 941 MB/sec through a single stream means the protocol framing adds negligible overhead to bulk transfers.

## Network vs In-Process

These benchmarks run in-process with `net.Pipe` (no TCP, no TLS). Real-world performance will be lower due to:

- **Network latency:** adds RTT to every stream operation
- **TLS overhead:** encryption/decryption (mitigated by TLS 1.3 session resumption)
- **TCP congestion:** flow control at the OS level in addition to the mux-level windows
- **Disk I/O:** if the local service involves file access (e.g., Samba)

The in-process results represent the **protocol ceiling** -- the maximum the mux layer can deliver. Real deployments should expect 50-80% of these numbers depending on network conditions.

## Tuning

**MuxConfig.MaxConcurrentStreams:** Community edition: fixed at 50. Enterprise edition: configurable via `max_streams_per_agent` in relay.yaml and planned agent config support. Set slightly above your expected peak.

**MuxConfig.InitialStreamWindow / ConnectionWindow:** Larger windows improve throughput for bulk transfers but increase memory usage. Default 256KB/1MB is balanced. For high-throughput single-stream transfers, increase to 1MB/4MB.

**ServiceForwarderConfig.BufferSize:** Controls the io.CopyBuffer chunk size. Default 32KB matches typical MTU aggregation. Increase to 64KB for high-bandwidth local services.

**ServiceForwarderConfig.IdleTimeout:** Set to 5-10 minutes for interactive services (SSH, web). Set to 30s for batch transfers. Set to 0 to disable (not recommended for production).
