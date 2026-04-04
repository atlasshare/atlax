# Step 6 Report: Enterprise Repo Setup

**Date:** 2026-04-03
**Repos:** atlax-enterprise (new), atlax (interface contract + patches)
**Enterprise PR:** initial commit (direct to main)
**Community PRs:** #77 (internal->pkg), #78 (interface contract), #80 (README), #81 (StartWithListener)
**Status:** COMPLETED

---

## Summary

Initialized the `atlax-enterprise` private repository at `github.com/atlasshare/atlax-enterprise`. Enterprise relay and agent entry points import community atlax v0.1.2 and wire MemoryRegistry, FileStore, SlogEmitter as placeholders for future enterprise implementations.

## Enterprise Repo Contents

- `go.mod` -- requires `github.com/atlasshare/atlax v0.1.2`
- `cmd/relay/main.go` -- mirrors community wiring with ENTERPRISE placeholder comments
- `cmd/agent/main.go` -- mirrors community wiring with ENTERPRISE placeholder comments
- `Makefile` -- build/test/lint targets, binary names `atlax-relay-enterprise` / `atlax-agent-enterprise`
- `.golangci.yml` -- copy of community with dual module prefixes
- `.github/workflows/ci.yml` -- build + test + lint with GOPRIVATE and golangci-lint-action v7
- `LICENSE` -- commercial (proprietary)
- `README.md` -- prerequisites, build instructions, relationship to community
- `.gitignore` -- bin/, coverage, go.work

## Community Patches

### v0.1.1 (PR #77)
Moved `internal/audit` and `internal/config` to `pkg/audit` and `pkg/config`. Go enforces `internal/` restrictions by module path -- the enterprise module path is not a child of the community path, so `internal/` packages were inaccessible. Updated all Go source, Makefile, Dockerfiles, CI workflow, GoReleaser.

### v0.1.2 (PR #81)
Added `AgentListener.StartWithListener(ctx, ln)` and `ClientListener.StartPortWithListener(ctx, ln, port)`. These accept pre-created listeners for enterprise fd passing. Existing `Start`/`StartPort` delegate to the new variants (no behavior change).

### Interface Contract (PR #78)
Created `docs/api/interfaces.md` documenting 7 stable interfaces, exported types, and stability rules (no method additions, new extension points use new interfaces).

## Design Decisions

1. **No CLAUDE.md in enterprise repo** -- all enterprise conventions live in workspace-level `atlax-department/CLAUDE.md`.
2. **go.work for local dev** -- `atlax-department/go.work` uses both repos. Not committed to either repo.
3. **golangci-lint v2.11.4 + action v7** -- v1.62.2 was built with Go 1.23 and cannot lint Go 1.25 code. Community issue #79 filed.
4. **Placeholder wiring** -- Enterprise main.go starts with community implementations, replaced one-at-a-time in Steps 7-8.

## CI Status

Enterprise CI green (build + test + lint) after fixing golangci-lint version (v1.62.2 -> v2.11.4) and action version (v6 -> v7).
