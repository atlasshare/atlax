# Step 3 Report: IdleTimeout for Forwarder

**Date:** 2026-04-02
**Branch:** `phase5/idle-timeout`
**PR:** pending
**Status:** COMPLETED

---

## Summary

Idle forwarding sessions are now closed after a configurable timeout. The `idleConn` wrapper resets the read/write deadline on every operation. If no data flows for `IdleTimeout`, the deadline fires and the connection closes.

**Changes:**
- `idleConn`: wraps net.Conn, calls SetDeadline before every Read/Write
- `newIdleConn(conn, timeout)`: returns original conn if timeout is 0 (backward compatible)
- `Forwarder.Forward`: wraps the local TCP connection with idleConn

## Decisions Made

1. **Explicit net.Conn implementation (not embedded)** -- Used explicit `conn` field instead of `net.Conn` embedding to avoid staticcheck QF1008 and prevent recursive method calls. All net.Conn methods delegated explicitly.
2. **Deadline on both Read and Write** -- Any data transfer resets the deadline. An idle session means no reads AND no writes.
3. **Zero timeout = disabled** -- `newIdleConn` returns the original conn unchanged. Existing configs without IdleTimeout work as before.

## Coverage Report

4 new tests: timeout on idle, active connection stays open, write resets deadline, zero timeout passthrough.
