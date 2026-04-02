# Performance Guide

## Overview

atlax includes a comprehensive in-process performance test suite that exercises the full protocol stack: stream multiplexing, bidirectional data transfer, flow control, and resource cleanup. Tests run without external infrastructure -- the relay and agent MuxSessions are connected via `net.Pipe` with an echo server as the local service.

## Running the Tests

```bash
# Run all benchmarks
go run ./scripts/loadtest/

# Run a specific benchmark
go run ./scripts/loadtest/ -bench load
go run ./scripts/loadtest/ -bench stress
go run ./scripts/loadtest/ -bench throughput
go run ./scripts/loadtest/ -bench latency
go run ./scripts/loadtest/ -bench churn
go run ./scripts/loadtest/ -bench ramp

# Run with race detector (slower but catches data races)
go run -race ./scripts/loadtest/ -bench load
```

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

**MuxConfig.MaxConcurrentStreams:** Set slightly above your expected peak. Too low rejects legitimate connections. Too high wastes memory on stream maps.

**MuxConfig.InitialStreamWindow / ConnectionWindow:** Larger windows improve throughput for bulk transfers but increase memory usage. Default 256KB/1MB is balanced. For high-throughput single-stream transfers, increase to 1MB/4MB.

**ServiceForwarderConfig.BufferSize:** Controls the io.CopyBuffer chunk size. Default 32KB matches typical MTU aggregation. Increase to 64KB for high-bandwidth local services.

**ServiceForwarderConfig.IdleTimeout:** Set to 5-10 minutes for interactive services (SSH, web). Set to 30s for batch transfers. Set to 0 to disable (not recommended for production).
