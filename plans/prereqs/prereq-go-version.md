# Prerequisite: Verify Go Version

## Why

The project requires Go 1.25+ (go.mod specifies 1.25.8). Phase 1 uses standard library features that must be available on your local toolchain. CI uses `go-version-file: go.mod` so it will match whatever go.mod declares.

## Steps

### 1. Check your current Go version

```bash
go version
```

Expected output: `go version go1.25.x darwin/arm64` (or later).

### 2. If Go is outdated, update it

**Option A: Homebrew (macOS)**

```bash
brew update
brew upgrade go
```

**Option B: Official installer**

Download from https://go.dev/dl/ and follow the installation instructions for your platform.

### 3. Verify after update

```bash
go version
# Should show go1.25.x or later

# Verify the project builds
cd ~/projects/atlax
go build ./...
go vet ./...
```

### 4. Confirm go.mod compatibility

```bash
head -2 go.mod
# Should show:
# module github.com/atlasshare/atlax
# go 1.25.8
```

If your local Go version is older than what go.mod declares, `go build` will fail with a version mismatch error.

## Done When

- `go version` shows 1.25.x or later
- `go build ./...` passes in the atlax directory
