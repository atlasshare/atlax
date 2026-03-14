# Contributing Guide

Thank you for considering contributing to atlax. This document describes the process for contributing code, the standards we follow, and how to get your changes merged.

---

## Getting Started

1. Fork the repository on GitHub.
2. Clone your fork locally:
   ```bash
   git clone https://github.com/<your-username>/atlax.git
   cd atlax
   ```
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/atlasshare/atlax.git
   ```
4. Set up the development environment by following [Getting Started](getting-started.md).

---

## Branch Naming

Create feature branches from `main` using the following prefixes:

| Prefix | Purpose | Example |
|--------|---------|---------|
| `feat/` | New features | `feat/udp-tunneling` |
| `fix/` | Bug fixes | `fix/stream-leak-on-reset` |
| `refactor/` | Code restructuring | `refactor/registry-interface` |
| `docs/` | Documentation changes | `docs/add-runbook-dns` |
| `test/` | Test additions or fixes | `test/flow-control-edge-cases` |
| `chore/` | Build, CI, dependencies | `chore/upgrade-golangci-lint` |
| `perf/` | Performance improvements | `perf/buffer-pool-reuse` |

```bash
git checkout -b feat/my-feature main
```

---

## Code Style

### Formatting

All Go code must be formatted with `gofmt`. The CI pipeline rejects unformatted code.

```bash
# Format all files
gofmt -w .

# Check formatting without writing
gofmt -l .
```

### Linting

We use [golangci-lint](https://golangci-lint.run/) with the project configuration in `.golangci.yml`. Run linting locally before submitting a pull request:

```bash
make lint
```

Address all lint warnings before submitting. If a lint rule produces a false positive, add a `//nolint:<rule>` comment with a brief explanation:

```go
//nolint:errcheck // Best-effort close on deferred cleanup; error is not actionable.
defer conn.Close()
```

### Code Conventions

- **Logging:** Use `log/slog` for all production logging. Never use `fmt.Println` or `log.Printf` in production code.
- **Context propagation:** Pass `context.Context` as the first parameter to all functions that perform I/O.
- **Error wrapping:** Wrap errors with context using `fmt.Errorf("operation: %w", err)`.
- **Naming:** Use explicit, self-describing names. Package names are lowercase single-word (protocol, relay, agent, auth, config, audit).
- **Immutability:** Prefer returning new objects over mutating existing ones.
- **File size:** Aim for 200-400 lines per file. Maximum 800 lines.
- **Function size:** Keep functions under 50 lines.
- **Nesting depth:** Avoid nesting deeper than 4 levels. Use early returns and extract helper functions.

---

## Commit Messages

We follow [Conventional Commits](https://www.conventionalcommits.org/) format:

```
<type>: <description>

<optional body>
```

### Types

| Type | When to Use |
|------|-------------|
| `feat` | A new feature |
| `fix` | A bug fix |
| `refactor` | Code restructuring without behavior change |
| `docs` | Documentation-only changes |
| `test` | Adding or modifying tests |
| `chore` | Build system, CI, tooling, dependencies |
| `perf` | Performance improvement |
| `ci` | CI pipeline changes |

### Examples

```
feat: add UDP stream multiplexing support

Implements UDP_BIND, UDP_DATA, and UDP_UNBIND commands
for tunneling UDP traffic over the existing TCP mux connection.
```

```
fix: prevent stream ID collision on concurrent opens

Race condition in stream ID allocation could assign the same
ID to two streams when opened simultaneously. Use atomic
increment instead of mutex-guarded counter.
```

```
test: add table-driven tests for frame decoder edge cases
```

### Rules

- Use lowercase for the description (no capitalized first letter).
- Do not end the description with a period.
- Use the imperative mood ("add feature" not "added feature").
- Keep the first line under 72 characters.
- Use the body to explain *why*, not *what* (the diff shows the what).

---

## Pull Request Process

### Before Submitting

1. Ensure your branch is up to date with `main`:
   ```bash
   git fetch upstream
   git rebase upstream/main
   ```

2. Run the full test suite with race detection:
   ```bash
   make test
   ```

3. Run the linter:
   ```bash
   make lint
   ```

4. Verify test coverage meets the 90% minimum:
   ```bash
   go test -coverprofile=coverage.out ./...
   go tool cover -func=coverage.out | tail -1
   ```

### Submitting

1. Push your branch to your fork:
   ```bash
   git push -u origin feat/my-feature
   ```

2. Open a pull request against `main` on the upstream repository.

3. Fill in the PR template:
   - Summary of changes (what and why)
   - Test plan (how to verify)
   - Related issues (if any)

### Review Process

- At least one maintainer approval is required.
- CI must pass (tests, lint, coverage).
- Address all review comments. Resolve conversations when the feedback is addressed.
- Squash-merge is the default merge strategy. Write a clear squash commit message.

### After Merge

- Delete your feature branch (GitHub does this automatically with the "Delete branch" option).
- Pull the latest `main`:
  ```bash
  git checkout main
  git pull upstream main
  ```

---

## Testing Requirements

All code changes must include tests. We target a minimum of 90% code coverage.

### Test Style

- **Table-driven tests:** Use table-driven tests for functions with multiple input/output combinations.
- **Race detection:** All tests run with the `-race` flag in CI.
- **Assertions:** Use `testify` for assertions where it improves readability.
- **Test files:** Place test files alongside the source file (`foo_test.go` next to `foo.go`).
- **Test naming:** Use `Test<Function>_<scenario>` naming (e.g., `TestFrameEncode_MaxPayload`).

### What to Test

- New functions and methods: unit tests
- New protocol behavior: integration tests with agent-relay interaction
- Bug fixes: a regression test that fails without the fix and passes with it
- Performance-sensitive changes: benchmark tests

See [Testing Strategy](testing.md) for detailed guidance.

---

## Documentation Requirements

### Code Changes

- Update or add doc comments for all exported types, functions, and methods.
- If the change affects the wire protocol, update the protocol documentation.
- If the change affects configuration, update the deployment documentation.

### Documentation-Only Changes

- One topic per file. Do not combine unrelated topics in a single document.
- Use clear headings and code examples.
- Keep operational and development documentation separate.

---

## Community and Enterprise Boundaries

atlax has a clear separation between Community Edition (this repository) and Enterprise features.

### Community Scope

All code in this repository is Apache 2.0 licensed and covers:

- Core wire protocol and multiplexing
- mTLS authentication
- Single-relay deployment
- In-memory agent registry
- Structured log audit output

### Enterprise Scope (out of scope for this repository)

Enterprise features are implemented in a separate private repository and injected via interfaces:

- **AgentRegistry** -- Community provides an in-memory implementation. Enterprise provides Redis/etcd.
- **Audit Emitter** -- Community provides structured log output. Enterprise provides event bus/SIEM integration.

### Contributing Guidelines for the Boundary

- Do not add Redis, etcd, or external state store dependencies to this repository.
- Do not add SIEM or event bus integrations to this repository.
- When adding a new extension point, define it as an interface in the appropriate `pkg/` package.
- Community implementations of interfaces go in the same package or in `internal/`.
- Enterprise implementations are injected at binary initialization time in `cmd/`.

---

## License

By contributing to atlax, you agree that your contributions will be licensed under the Apache License 2.0.

All source files must include the license header. The CI pipeline checks for this.
