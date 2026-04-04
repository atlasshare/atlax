# Step 7a Report: Zero-Downtime Relay Binary Swap

**Date:** 2026-04-04
**Branch:** `phase6/fd-passing` (enterprise repo)
**PR:** atlax-enterprise#1
**Status:** IN REVIEW (pending merge)

---

## Summary

Implemented zero-downtime binary swap for the enterprise relay via SIGUSR2-triggered fd passing. On SIGUSR2, the running relay forks/execs the new binary with inherited listening file descriptors, waits for the new process to signal readiness, then drains connections and exits.

## New Files (enterprise)

### pkg/graceful/fdenv.go
LISTEN_FDS environment helpers following the systemd socket activation protocol with an "inherit" PID extension. Functions:
- `IsInherited()` -- checks LISTEN_FDS + LISTEN_PID (supports both systemd PID match and "inherit" magic value)
- `GetListenFDs()` -- recovers fds starting at fd 3, parses LISTEN_FDNAMES, clears env to prevent grandchild inheritance
- `SetListenFDs()` -- builds env vars and extra files for child process

### pkg/graceful/restart.go
Restart orchestrator. Key components:
- `Restarter` -- holds listeners, binary path, mutex guard against double restart
- `Restart(ctx)` -- extracts fds from listeners, creates readiness pipe, fork/execs new binary, waits for ready signal
- `ListenerFile(ln)` -- extracts `*os.File` from any listener implementing `File()`
- `SignalReady(w)` / `WaitForReady(ctx, r)` -- pipe-based readiness protocol
- `mergeEnv()` -- overlays LISTEN_FDS vars onto inherited environment

### cmd/relay/main.go changes
- SIGUSR2 registered alongside SIGINT/SIGTERM
- `startFresh()` -- creates listeners externally (not via Relay.Start), populates restarter with all fds
- `startInherited()` -- recovers fds from GetListenFDs, wraps agent fd in TLS, starts components via StartWithListener
- `signalReadyToParent()` -- writes to READY_PIPE_FD on startup if present
- Failed restart does not crash the running process (logs error, continues)

## Community Dependencies

- v0.1.2: `AgentListener.StartWithListener(ctx, ln)` and `ClientListener.StartPortWithListener(ctx, ln, port)`

## Design Decisions

1. **LISTEN_PID=inherit** -- Parent cannot know child PID before os.StartProcess (env is set at fork time). Used "inherit" magic value instead of systemd's PID match. Child accepts both.
2. **startFresh vs Relay.Start** -- On fresh start, enterprise creates listeners externally so the restarter can track them. Relay.Start creates listeners internally and does not expose them.
3. **Mutex guard** -- Second SIGUSR2 during active restart is rejected. Prevents fork bombs.
4. **Deferred fd cleanup** -- `startInherited` tracks consumed fds and closes unconsumed ones on error (code review finding).
5. **Readiness pipe** -- Parent reads, child writes. Parent kills child if readiness timeout fires. Child signals after all listeners are active.

## Coverage

`pkg/graceful`: 88% (16 tests covering fd env helpers, listener file extraction, readiness pipe, double restart guard, bad binary error, mergeEnv)

## Code Review Findings (addressed)

| Severity | Issue | Fix |
|----------|-------|-----|
| HIGH | FD leak on inherited path errors | Deferred cleanup with consumed[] tracking |
| HIGH | Empty listener list on fresh start | New startFresh() creates listeners externally |
| MEDIUM | Ignored port mapping errors | Fail startup on AddPortMapping error |
