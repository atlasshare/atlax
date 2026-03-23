# Prerequisite: Verify Local golangci-lint Setup

## Why

Phase 1 introduces substantial Go code. Running the linter locally before pushing catches issues early and avoids CI round-trip delays. The project uses golangci-lint v2, which has a different configuration format than v1.

## Steps

### 1. Check if golangci-lint is installed

```bash
golangci-lint version
```

Expected: `golangci-lint has version v2.11.3` (or later v2.x).

### 2. Install or update golangci-lint

**If not installed or on v1.x:**

```bash
# macOS
brew install golangci-lint

# Or via Go install (any platform)
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

**If installed but outdated v2.x:**

```bash
brew upgrade golangci-lint
# Or:
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

### 3. Verify it works with the project config

```bash
cd ~/projects/atlax
golangci-lint run ./...
```

This should complete without errors (the scaffold has no linting issues).

### 4. Verify the config file format

```bash
head -5 .golangci.yml
```

If the file starts with `version: "2"` or uses `linters.enable` format, it is v2 format. If it uses `linters-settings` at the top level, it may be v1 format. The project should be on v2.

### 5. Test with a deliberate violation (optional)

Create a temporary file to confirm the linter catches issues:

```bash
cat > /tmp/lint-test.go << 'EOF'
package protocol

import "fmt"

func unused() {
    fmt.Println("test")
}
EOF

cp /tmp/lint-test.go pkg/protocol/lint_test_temp.go
golangci-lint run ./pkg/protocol/...
# Should report errors (unused function, fmt.Println)
rm pkg/protocol/lint_test_temp.go
```

## Troubleshooting

**"golangci-lint: command not found":**

Make sure `$GOPATH/bin` or `$HOME/go/bin` is in your PATH:

```bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**Config format errors (v1 vs v2):**

If you see errors about config format, check:
```bash
cat .golangci.yml | head -1
```
If it says `version: "2"`, ensure you have golangci-lint v2.x installed. v1.x cannot read v2 configs.

**"typecheck" errors:**

This usually means Go version mismatch. Ensure your local Go version matches go.mod:
```bash
go version
head -2 go.mod
```

## Done When

- `golangci-lint version` shows v2.11.x or later
- `golangci-lint run ./...` passes in the atlax directory
- No config format errors
