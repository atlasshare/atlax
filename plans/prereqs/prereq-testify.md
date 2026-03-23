# Prerequisite: Add Testify Dependency

## Why

Phase 1 tests use `github.com/stretchr/testify` for assertions and require checks per CLAUDE.md conventions. The scaffold phase intentionally had zero external dependencies, but Phase 1 is the right time to add the test framework.

## Steps

### 1. Add testify to go.mod

```bash
cd ~/projects/atlax
go get github.com/stretchr/testify@latest
```

This will update `go.mod` and create/update `go.sum`.

### 2. Verify the dependency was added

```bash
grep testify go.mod
# Should show: require github.com/stretchr/testify vX.Y.Z
```

### 3. Tidy modules

```bash
go mod tidy
```

### 4. Verify everything still builds

```bash
go build ./...
go vet ./...
```

### 5. Commit the dependency addition

```bash
git checkout -b chore/add-testify
git add go.mod go.sum
git commit -m "chore: add testify dependency for Phase 1 tests"
git push -u origin chore/add-testify
```

Create and merge the PR before starting Phase 1 implementation.

## Done When

- `go.mod` contains a `require` block with `github.com/stretchr/testify`
- `go.sum` is populated with testify and its transitive dependencies
- `go build ./...` still passes
- PR merged to main
