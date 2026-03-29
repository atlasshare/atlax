# Prerequisite: Verify Port Availability

## Why

Phase 3 integration tests start real TCP listeners on specific ports. If another process holds these ports, tests will fail with "address already in use".

## Steps

```bash
# Check if any of the test ports are in use
lsof -i :8080 -i :8081 -i :8443 -i :9090
```

If any are in use, either stop the conflicting process or note that integration tests should use random ports (`:0`).

Most tests use `:0` (OS-assigned random port) by default. This check is primarily for the end-to-end smoke test in Step 6 which may use fixed ports from relay.example.yaml.

## Done When

- No critical conflicts on ports 8080, 8081, 8443
- Or: confirmed that all tests use `:0` for port allocation
