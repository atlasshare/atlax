# Prerequisite: Add Prometheus Client Dependency

## Why

Phase 4 Step 3 uses Prometheus counters and gauges for per-customer metrics.

## Steps

```bash
cd ~/projects/atlax
go get github.com/prometheus/client_golang/prometheus
go get github.com/prometheus/client_golang/prometheus/promhttp
go mod tidy
go build ./...
go test -race ./...
```

Commit on a `chore/add-prometheus-dep` branch and merge before Phase 4.

## Done When

- `go.mod` contains `github.com/prometheus/client_golang`
- All tests pass
