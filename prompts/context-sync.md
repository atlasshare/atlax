# Context Sync: atlax Codebase Orientation

You are an agent joining the atlax project. Your goal is to build an accurate mental model of what exists in the codebase, what is implemented vs scaffolded, and what the next implementation step is.

## Project Identity

- **Name:** atlax -- Custom reverse TLS tunnel with TCP stream multiplexing in Go
- **Module:** `github.com/atlasshare/atlax`
- **Repo:** https://github.com/atlasshare/atlax
- **Parent product:** AtlasShare (relay component, Community Edition)
- **License:** Apache 2.0

## Step 1: Read Foundational Documents (DO THIS FIRST)

Read these files in parallel to understand the project contract:

1. `CLAUDE.md` -- Project conventions, architecture, code standards, security rules
2. `docs/reference/action-plan.md` -- Wire protocol spec, implementation phases, architecture diagrams. **Important:** This file contains BOTH an original 6-phase plan and a superseding "Updated Implementation Timeline" table near the end with 8 phases. The 8-phase version governs. Also note: the project structure diagram in this file uses the placeholder root name `atlasshare-relay/` -- the actual repo root is `atlax/`.
3. `plans/atlax-scaffold-construction-plan.md` -- Scaffold blueprint. Read to confirm the full list of files that were intentionally scaffolded, so you can identify if any expected files are missing.
4. `go.mod` -- Module path, Go version, dependencies

## Step 2: Audit Implementation State

For each package below, read ALL `.go` files (skip `doc.go` files -- they contain only the package comment and have no types or functions to classify) and classify every type, function, and method as one of:

| Status | Meaning |
|--------|---------|
| **INTERFACE** | Type or method signature only, no concrete implementation |
| **STUB** | Function body exists but contains `// TODO` or returns nil/zero |
| **IMPLEMENTED** | Real logic that does meaningful work |

### Packages to audit:

```
pkg/protocol/   -- Wire protocol: frame types, stream, mux, errors
pkg/auth/        -- mTLS config, identity extraction, cert management
pkg/relay/       -- Relay server: listener, registry, router
pkg/agent/       -- Tunnel agent: client, forwarder, tunnel
internal/config/ -- Configuration loading
internal/audit/  -- Audit event emission
cmd/relay/       -- Relay binary entry point
cmd/agent/       -- Agent binary entry point
```

### Check for test files

Use Glob to search for `**/*_test.go` in the project root directory.

If none exist, note that explicitly -- test coverage is currently 0%.

### Check for external dependencies

Read `go.mod`. If the only entry is the module declaration and Go version with no `require` blocks, the project uses stdlib only.

### Audit CI/CD workflows

Read all files in `.github/workflows/` to understand the CI pipeline, what jobs run, and what tools are used (linters, security scanners, build matrix).

## Step 3: Identify Current Phase

Read the "Updated Implementation Timeline" table in `docs/reference/action-plan.md` (near the end of the file). This is the authoritative phase list:

- **Phase 1: Core Protocol** -- Mux library with TCP + UDP framing
- **Phase 2: Agent** -- Agent binary with mTLS, reconnection
- **Phase 3: Relay (single)** -- Single-node relay with routing
- **Phase 4: Multi-tenancy** -- Customer isolation, port allocation
- **Phase 5: Self-update** -- Agent auto-update system
- **Phase 6: Cert rotation** -- Automated certificate renewal
- **Phase 7: HA (active-active)** -- Redis-backed registry, cross-relay routing
- **Phase 8: Production hardening** -- Metrics, logging, load testing

Cross-reference with your Step 2 audit. If every Go file is an interface or stub with no concrete logic, the correct classification is: **"Scaffold complete, Phase 1 not started."** This is the expected state for a project that has completed its scaffold construction and is about to begin real implementation. Do not classify this as "no phase" or "Phase 0."

## Step 4: Map the Next Step Forward

Based on your audit, identify:

1. **Current state:** What phase is the project in? What exists as real implementation?
2. **Next deliverable:** What is the immediate next thing to build? (Reference the Phase 1 checklist items from the action plan.)
3. **First files to touch:** Which existing stub/interface files will receive real implementations?
4. **Dependencies:** What (if any) external packages will be needed?
5. **Test plan:** What test files need to be created alongside the implementation?

## Step 5: Produce a Context Report

Output a structured report with these sections:

```
## Codebase State Summary

### Implementation Inventory
[Table: Package | File | Types/Functions | Status (INTERFACE/STUB/IMPLEMENTED)]

### Test Coverage
[Current coverage status, list of existing test files or "none"]

### External Dependencies
[List from go.mod require blocks, or "stdlib only"]

### CI/CD Status
[List each workflow file, its trigger, and jobs. Note current pipeline health if determinable.]

### Current Phase
[Which implementation phase the project is at, with evidence from the file audit]

### Next Step
[Concrete description of what to build next, which files, what tests]

### Key Constraints
The next implementer must follow these rules from CLAUDE.md. Verify each is addressed:
- [ ] Structured logging with log/slog only (no fmt.Println)
- [ ] Propagate context.Context through all I/O functions
- [ ] Prefer immutability; flag mutations for human review
- [ ] Wrap errors with fmt.Errorf("operation: %w", err)
- [ ] Functions under 50 lines, files under 800 lines
- [ ] TLS 1.3 minimum, no plaintext connections
- [ ] No cross-tenant routing -- verify customer ID on every stream
- [ ] No hardcoded secrets, passwords, or private keys
- [ ] Audit all connection and stream lifecycle events
- [ ] No emoji anywhere (code, comments, docs, commits)
- [ ] No co-author or generated-by lines in commit messages
- [ ] Table-driven tests with -race flag, 90% coverage target
- [ ] Use testify for assertions where helpful
```

## Important Rules

- Do NOT modify any files. This is a read-only orientation task.
- Do NOT guess or assume -- read the actual files and report what you find.
- If something contradicts the docs (e.g., a file exists that docs say should not), flag it.
- Reference specific file paths and line numbers when citing evidence.
- Follow the project's no-emoji rule in your output.
