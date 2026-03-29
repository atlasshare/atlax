# Prerequisite: Add YAML Configuration Dependency

## Why

Phase 2 Step 5 implements configuration loading for the agent binary. The agent reads its config from a YAML file (`agent.yaml`). The `gopkg.in/yaml.v3` package is the standard Go YAML library and is needed by `internal/config/`.

## Steps

### 1. Add yaml.v3 to go.mod

```bash
cd ~/projects/atlax
go get gopkg.in/yaml.v3@latest
```

### 2. Verify the dependency was added

```bash
grep yaml go.mod
# Should show: require gopkg.in/yaml.v3 vX.Y.Z (or in indirect block)
```

### 3. Tidy modules

```bash
go mod tidy
```

### 4. Verify everything still builds

```bash
go build ./...
go vet ./...
go test -race ./...
```

### 5. Commit the dependency addition

```bash
git checkout -b chore/add-yaml-dep
git add go.mod go.sum
git commit -m "chore: add gopkg.in/yaml.v3 for Phase 2 config loading"
git push -u origin chore/add-yaml-dep
```

Create and merge the PR before starting Phase 2 implementation.

## Done When

- `go.mod` contains `gopkg.in/yaml.v3`
- `go build ./...` still passes
- All existing tests still pass
- PR merged to main
