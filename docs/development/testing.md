# Testing Strategy

This document describes the testing approach for atlax, covering unit tests, integration tests, load tests, tooling, coverage requirements, naming conventions, mocking, and CI configuration.

---

## Test Pyramid

```
            /  E2E / Load  \          Slow, expensive, high confidence
           /----------------\
          / Integration Tests \       Agent-relay interaction, cert flows
         /--------------------\
        /     Unit Tests       \      Frame encoding, state machines, utilities
       /________________________\     Fast, cheap, foundational
```

All three layers are required. Unit tests form the foundation; integration and load tests verify that the system works end-to-end under realistic conditions.

---

## Unit Tests

Unit tests verify individual functions, types, and state machines in isolation.

### What to Unit Test

| Component | Test Focus |
|-----------|-----------|
| `pkg/protocol/frame.go` | Frame encoding/decoding, field boundaries, endianness, maximum payload size, version handling |
| `pkg/protocol/stream.go` | Stream state machine transitions (OPEN, DATA, CLOSE, RESET), invalid transition rejection |
| `pkg/protocol/mux.go` | Stream ID allocation (odd/even), flow control window accounting, WINDOW_UPDATE handling |
| `pkg/auth/` | Certificate parsing, customer ID extraction from CN, CA chain validation logic |
| `internal/config/` | YAML parsing, environment variable overrides, validation of required fields |

### Example: Table-Driven Test

```go
func TestFrameEncode(t *testing.T) {
    tests := []struct {
        name    string
        frame   Frame
        want    []byte
        wantErr bool
    }{
        {
            name: "ping frame with no payload",
            frame: Frame{
                Version:  0x01,
                Command:  CmdPing,
                Flags:    0x00,
                StreamID: 0,
                Payload:  nil,
            },
            want: []byte{
                0x01, 0x05, 0x00, 0x00,   // version, command, flags, reserved
                0x00, 0x00, 0x00, 0x00,   // stream ID
                0x00, 0x00, 0x00, 0x00,   // payload length
            },
            wantErr: false,
        },
        {
            name: "data frame with payload",
            frame: Frame{
                Version:  0x01,
                Command:  CmdStreamData,
                Flags:    0x00,
                StreamID: 1,
                Payload:  []byte("hello"),
            },
            want: append(
                []byte{
                    0x01, 0x02, 0x00, 0x00,
                    0x00, 0x00, 0x00, 0x01,
                    0x00, 0x00, 0x00, 0x05,
                },
                []byte("hello")...,
            ),
            wantErr: false,
        },
        {
            name:    "payload exceeds maximum size",
            frame:   Frame{Payload: make([]byte, MaxPayloadSize+1)},
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := tt.frame.Encode()
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

### State Machine Tests

Test every valid and invalid state transition:

```go
func TestStreamStateMachine(t *testing.T) {
    tests := []struct {
        name         string
        initialState StreamState
        event        StreamEvent
        wantState    StreamState
        wantErr      bool
    }{
        {"open to data", StateOpen, EventData, StateOpen, false},
        {"open to close", StateOpen, EventClose, StateClosed, false},
        {"open to reset", StateOpen, EventReset, StateReset, false},
        {"closed rejects data", StateClosed, EventData, StateClosed, true},
        {"reset rejects close", StateReset, EventClose, StateReset, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            sm := NewStreamStateMachine(tt.initialState)
            err := sm.Transition(tt.event)
            if tt.wantErr {
                require.Error(t, err)
            } else {
                require.NoError(t, err)
            }
            assert.Equal(t, tt.wantState, sm.State())
        })
    }
}
```

---

## Integration Tests

Integration tests verify the interaction between the relay and agent over real TLS connections using development certificates.

### What to Integration Test

| Scenario | Description |
|----------|-------------|
| Agent-relay handshake | Agent connects to relay, completes mTLS, registers successfully |
| Stream forwarding | Client connects to relay service port, data flows through tunnel to local service |
| Bidirectional data | Data flows in both directions through a stream |
| Agent reconnection | Agent disconnects and reconnects, relay updates registry |
| Certificate rejection | Agent with an invalid or expired certificate is rejected |
| GOAWAY handling | Relay sends GOAWAY, agent stops opening new streams and reconnects |
| Multiple agents | Multiple agents connect simultaneously, each routed correctly |
| Stream limit enforcement | Relay rejects stream opens beyond the configured per-agent limit |

### Test Setup

Integration tests create real TLS listeners and clients using development certificates. Tests use ephemeral ports (`:0`) to avoid port conflicts.

```go
func TestAgentRelayHandshake(t *testing.T) {
    // Start relay with test certificates on ephemeral port
    relay := startTestRelay(t, testRelayCert, testRelayKey, testCustomerCA)
    defer relay.Close()

    // Connect agent with test client certificate
    agent := startTestAgent(t, relay.Addr(), testAgentCert, testAgentKey, testRelayCA)
    defer agent.Close()

    // Verify agent is registered
    require.Eventually(t, func() bool {
        return relay.AgentCount() == 1
    }, 5*time.Second, 100*time.Millisecond)
}
```

### Certificate Rejection Tests

```go
func TestRejectInvalidCertificate(t *testing.T) {
    relay := startTestRelay(t, testRelayCert, testRelayKey, testCustomerCA)
    defer relay.Close()

    // Attempt connection with a certificate signed by an untrusted CA
    _, err := connectAgent(relay.Addr(), untrustedCert, untrustedKey, testRelayCA)
    require.Error(t, err)
    assert.Contains(t, err.Error(), "certificate")
}
```

---

## Load Tests

Load tests verify the system's behavior under sustained high concurrency to validate scaling targets.

### Scaling Targets

| Metric | Target |
|--------|--------|
| Concurrent agents | 1,000 |
| Streams per agent | 100 |
| Total concurrent streams | 100,000 |
| Throughput per stream | 100 Mbps |
| Relay memory | < 4 GB for 1,000 agents |
| Handshake latency | < 50 ms (with session resumption) |
| Sustained duration | 24 hours |

### Load Test Scenarios

**Scenario 1: Agent Connection Storm**

Simulate 1,000 agents connecting within a 60-second window. Measure handshake latency distribution and relay memory usage during the ramp-up.

**Scenario 2: Sustained Stream Throughput**

With 100 agents connected, each opens 100 streams. Each stream transfers data bidirectionally at a steady rate. Run for 1 hour. Measure throughput degradation and memory stability.

**Scenario 3: Chaos Testing**

With agents and streams at load, inject failures:
- Random agent disconnects (10% per minute)
- Network latency injection (100ms added)
- Packet loss (1%)
- Relay restart with GOAWAY

Measure reconnection time, stream recovery, and data integrity.

### Tools

| Tool | Purpose |
|------|---------|
| Custom Go load generator | Agent simulation with mTLS, stream creation, data transfer |
| `k6` | HTTP-level load testing for control plane endpoints |
| `toxiproxy` | Network fault injection (latency, packet loss, connection reset) |

### Running Load Tests

```bash
# Run the load test script (requires relay running)
./scripts/load-test.sh --agents 100 --streams-per-agent 50 --duration 30m
```

---

## Testing Tools

### go test

The primary test runner. All tests are invoked through `go test`.

```bash
# Run all tests with race detection
go test -race ./...

# Run tests for a specific package
go test -race ./pkg/protocol/...

# Run a specific test
go test -race -run TestFrameEncode ./pkg/protocol/

# Run with verbose output
go test -race -v ./pkg/protocol/
```

### testify

We use `github.com/stretchr/testify` for assertions and requirements:

- `assert` -- reports failure but continues the test
- `require` -- reports failure and stops the test immediately

Use `require` for preconditions that make subsequent assertions meaningless if they fail. Use `assert` for independent checks.

### toxiproxy

[toxiproxy](https://github.com/Shopify/toxiproxy) injects network faults between the agent and relay for resilience testing:

```go
func TestAgentReconnectsAfterLatencySpike(t *testing.T) {
    // Set up toxiproxy between agent and relay
    proxy := toxiproxy.NewProxy("relay", relayAddr)
    proxy.AddToxic("latency", "latency", "", 1, toxiproxy.Attributes{
        "latency": 5000,  // 5 second latency
    })
    // ... verify agent reconnects
}
```

### k6

[k6](https://k6.io/) is used for load testing HTTP endpoints (health checks, metrics, control plane API):

```javascript
import http from 'k6/http';
import { check } from 'k6';

export const options = {
    vus: 50,
    duration: '5m',
};

export default function () {
    const res = http.get('http://localhost:8080/healthz');
    check(res, {
        'status is 200': (r) => r.status === 200,
        'response time < 100ms': (r) => r.timings.duration < 100,
    });
}
```

---

## Coverage

### Target

Minimum 90% code coverage across the project. Coverage is enforced in CI via Codecov.

### Measuring Coverage

```bash
# Generate coverage profile
go test -race -coverprofile=coverage.out ./...

# View summary
go tool cover -func=coverage.out | tail -1

# View detailed HTML report
go tool cover -html=coverage.out -o coverage.html
open coverage.html
```

### Coverage Exclusions

Some code is difficult to unit test and may have lower coverage:

- `cmd/` entry points (minimal logic, tested via integration tests)
- Fatal error paths that call `os.Exit`
- Platform-specific code paths

These exclusions must be justified and documented. The overall project coverage must still meet the 90% threshold.

---

## Naming Conventions

### Test Functions

```
Test<Type>_<Method>_<Scenario>
```

Examples:

```go
func TestFrame_Encode_PingWithNoPayload(t *testing.T) {}
func TestFrame_Decode_InvalidVersion(t *testing.T) {}
func TestStream_Transition_OpenToClose(t *testing.T) {}
func TestRegistry_Register_DuplicateCustomerID(t *testing.T) {}
```

### Test Files

Test files are placed alongside the source file they test:

```
pkg/protocol/frame.go
pkg/protocol/frame_test.go
pkg/protocol/stream.go
pkg/protocol/stream_test.go
```

### Benchmark Functions

```
Benchmark<Type>_<Method>
```

Example:

```go
func BenchmarkFrame_Encode(b *testing.B) {}
func BenchmarkFrame_Decode(b *testing.B) {}
```

---

## Mocking

### When to Mock

- External dependencies (network connections, file system, time)
- Interfaces that have multiple implementations (AgentRegistry, Audit Emitter)
- Slow or non-deterministic operations

### When Not to Mock

- Pure functions (frame encoding, state machine transitions)
- In-memory data structures
- The code under test itself

### Mocking Approach

Define interfaces at consumption boundaries and provide test implementations:

```go
// Production interface
type AgentRegistry interface {
    Register(customerID string, conn *AgentConn) error
    Lookup(customerID string) (*AgentConn, error)
}

// Test implementation
type mockRegistry struct {
    agents map[string]*AgentConn
}

func (m *mockRegistry) Register(customerID string, conn *AgentConn) error {
    m.agents[customerID] = conn
    return nil
}

func (m *mockRegistry) Lookup(customerID string) (*AgentConn, error) {
    conn, ok := m.agents[customerID]
    if !ok {
        return nil, ErrAgentNotFound
    }
    return conn, nil
}
```

We do not use code generation mocking frameworks. Hand-written mocks are preferred for clarity.

---

## CI Pipeline

### Test Stage

The CI pipeline runs the following checks on every pull request:

1. **Formatting:** `gofmt -l .` (fails if any file is not formatted)
2. **Linting:** `golangci-lint run`
3. **Unit tests:** `go test -race -coverprofile=coverage.out ./...`
4. **Coverage:** Upload `coverage.out` to Codecov, enforce 90% minimum
5. **Integration tests:** Run with `-tags integration` flag

### Race Detector

All tests run with the `-race` flag. The race detector catches data races at test time that would be difficult to diagnose in production. Any race detected causes the test to fail.

### Codecov

Coverage reports are uploaded to Codecov on every push. The Codecov configuration enforces:

- Project coverage must not drop below 90%
- Patch coverage (new code in the PR) must be at least 90%
- Coverage regressions are flagged as check failures

### Running CI Locally

Reproduce the CI pipeline locally before pushing:

```bash
# Full CI check
gofmt -l .
make lint
make test
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
```
