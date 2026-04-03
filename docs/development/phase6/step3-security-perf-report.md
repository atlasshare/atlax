# Step 3 Report: Security + Performance

**Date:** 2026-04-02
**Branch:** `phase6/security-perf`
**PR:** pending
**Status:** COMPLETED

---

## Summary

**Fuzz testing (closes #33):** Two fuzz targets added:
- `FuzzReadFrame`: feeds random bytes into the frame decoder. 356K executions in 10s, zero crashes. Round-trips valid frames to verify integrity.
- `FuzzParseUDPDataPayload`: feeds random bytes into the UDP parser. 539K executions in 10s, zero crashes.

Seed corpus includes all commands, flags, edge cases (empty, truncated, invalid version, max stream ID).

**sync.Pool evaluation (closes #32):** Benchmark shows pool reduces decode allocations from 4192 B/op to 48 B/op (87x improvement) and latency from 664ns to 149ns (4.5x faster). However, implementing pool requires changing frame ownership semantics (callers must return frames). Decision: **defer implementation, document the data.** The load test shows 0% errors at 1000 streams without pool. Pool becomes worthwhile at 10,000+ concurrent streams.

**Agent-stack benchmark (#60):** Deferred to a separate PR. Requires wiring TunnelClient + TunnelRunner into the loadtest tool.

## Benchmark Results

```
BenchmarkDecodeFrame (no pool):    664 ns/op    4192 B/op    4 allocs/op
BenchmarkDecodeFrame (with pool):  149 ns/op      48 B/op    1 allocs/op

Improvement: 4.5x faster, 87x less memory, 4x fewer allocations
```

## Decisions Made

1. **Fuzz targets verify round-trip** -- If the fuzzer produces bytes that decode into a valid Frame, the test round-trips it (encode -> decode -> compare). This catches asymmetric encode/decode bugs.
2. **sync.Pool deferred** -- Data proves the optimization works but it changes the API contract (frame ownership). At current load levels (1000 streams), GC handles 74MB/sec of decode allocations fine. Implement when targeting 10K+ concurrent streams.
