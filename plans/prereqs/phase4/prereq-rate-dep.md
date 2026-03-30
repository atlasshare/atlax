# Prerequisite: Add Rate Limiter Dependency

## Why

Phase 4 Step 2 uses `golang.org/x/time/rate` for per-source-IP token bucket rate limiting.

## Steps

```bash
cd ~/projects/atlax
go get golang.org/x/time
go mod tidy
go build ./...
go test -race ./...
```

Commit on a `chore/add-rate-dep` branch and merge before Phase 4.

## Done When

- `go.mod` contains `golang.org/x/time`
- All tests pass
