// loadtest is a comprehensive performance test suite for atlax.
//
// Two modes:
//
// In-process mode (default): relay and agent MuxSessions connected via
// net.Pipe with an echo server. Measures the protocol ceiling.
//
// Remote mode (--remote): connects to a live relay's client port over
// real TCP. Measures actual production performance including network
// latency, TLS overhead, and service response time.
//
// Usage:
//
//	go run ./scripts/loadtest/                                          # all in-process benchmarks
//	go run ./scripts/loadtest/ -bench load                              # specific benchmark
//	go run ./scripts/loadtest/ -remote 18.207.237.252:18080 -bench load # against live relay
//	go run ./scripts/loadtest/ -remote 18.207.237.252:18080 -bench ramp # ramp test against production
//	go run -race ./scripts/loadtest/ -bench load                        # with race detector

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

var remoteAddr string

func main() {
	bench := flag.String("bench", "all", "benchmark: all, load, stress, throughput, latency, churn, ramp")
	remote := flag.String("remote", "", "remote relay address (e.g., 18.207.237.252:18080) for production benchmarking")
	flag.Parse()

	remoteAddr = *remote

	if remoteAddr != "" {
		fmt.Fprintf(os.Stderr, "atlax performance test suite (REMOTE: %s)\n", remoteAddr)
	} else {
		fmt.Fprintf(os.Stderr, "atlax performance test suite (IN-PROCESS)\n")
	}
	fmt.Fprintf(os.Stderr, "============================================\n\n")

	switch *bench {
	case "all":
		runLoad()
		if remoteAddr == "" {
			runStress()
			runThroughput()
		}
		runLatency()
		if remoteAddr == "" {
			runChurn()
		}
		runRamp()
	case "load":
		runLoad()
	case "stress":
		runStress()
	case "throughput":
		runThroughput()
	case "latency":
		runLatency()
	case "churn":
		runChurn()
	case "ramp":
		runRamp()
	default:
		fmt.Fprintf(os.Stderr, "unknown benchmark: %s\n", *bench)
		os.Exit(1)
	}
}

// --- Infrastructure ---

func newMuxPair(maxStreams int) (relay, agent *protocol.MuxSession, cleanup func()) {
	relayConn, agentConn := net.Pipe()
	cfg := protocol.MuxConfig{
		MaxConcurrentStreams: maxStreams + 100,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576 * 4,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	}
	relay = protocol.NewMuxSession(relayConn, protocol.RoleRelay, cfg)
	agent = protocol.NewMuxSession(agentConn, protocol.RoleAgent, cfg)
	cleanup = func() {
		relay.Close()
		agent.Close()
	}
	return
}

func startEchoServer() (ln net.Listener, cleanup func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn) //nolint:errcheck // echo server, errors not actionable
			}()
		}
	}()
	return ln, func() { ln.Close() }
}

func startAgentForwarder(ctx context.Context, agentMux *protocol.MuxSession, echoAddr string) {
	go func() {
		for {
			stream, err := agentMux.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func(s protocol.Stream) {
				local, dialErr := net.DialTimeout("tcp", echoAddr, 5*time.Second)
				if dialErr != nil {
					return
				}
				defer local.Close()
				defer s.Close()
				done := make(chan struct{}, 2)
				go func() { io.Copy(local, s); done <- struct{}{} }() //nolint:errcheck // forwarder, errors not actionable
				go func() { io.Copy(s, local); done <- struct{}{} }() //nolint:errcheck // forwarder, errors not actionable
				select {
				case <-done:
				case <-ctx.Done():
				}
			}(stream)
		}
	}()
}

func makePayload(size int) []byte {
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	return buf
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// --- Remote TCP round-trip (production mode) ---

// remoteTCPRoundTrip connects to the relay's client port, sends msg,
// reads the echo response, and closes. This exercises the full
// production path: client TCP -> relay -> mux -> agent -> local service.
func remoteTCPRoundTrip(ctx context.Context, addr string, msg []byte) error {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Set deadline for the entire round-trip
	conn.SetDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck // best-effort

	if _, err := conn.Write(msg); err != nil {
		return err
	}

	// For HTTP services, read whatever comes back (we don't know exact size).
	// For echo services, read exactly len(msg).
	buf := make([]byte, len(msg)+4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return err
	}
	if n == 0 {
		return fmt.Errorf("empty response")
	}
	return nil
}

// --- Benchmarks ---

func runLoad() {
	const streams = 1000
	const msgSize = 1024

	fmt.Fprintf(os.Stderr, "[load] %d concurrent streams, %d bytes each\n", streams, msgSize)

	if remoteAddr != "" {
		runRemoteLoad(streams, msgSize)
		return
	}

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()
	relayMux, agentMux, muxCleanup := newMuxPair(streams)
	defer muxCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	startAgentForwarder(ctx, agentMux, echoLn.Addr().String())

	msg := makePayload(msgSize)
	var wg sync.WaitGroup
	var ok, fail atomic.Int64

	start := time.Now()
	for range streams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := echoRoundTrip(ctx, relayMux, msg); err != nil {
				fail.Add(1)
			} else {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	printResult("load", ok.Load(), fail.Load(), elapsed)
}

func runRemoteLoad(streams, msgSize int) {
	msg := makePayload(msgSize)
	var wg sync.WaitGroup
	var ok, fail atomic.Int64

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	for range streams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := remoteTCPRoundTrip(ctx, remoteAddr, msg); err != nil {
				fail.Add(1)
			} else {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	printResult("load/remote", ok.Load(), fail.Load(), elapsed)
}

func runStress() {
	if remoteAddr != "" {
		fmt.Fprintf(os.Stderr, "[stress] skipped in remote mode (would DoS the relay)\n\n")
		return
	}

	fmt.Fprintf(os.Stderr, "[stress] finding breaking point (batches of 500)\n")

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()

	msg := makePayload(512)

	for n := 500; n <= 5000; n += 500 {
		relayMux, agentMux, muxCleanup := newMuxPair(n + 200)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		startAgentForwarder(ctx, agentMux, echoLn.Addr().String())

		var wg sync.WaitGroup
		var ok, fail atomic.Int64

		start := time.Now()
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := echoRoundTrip(ctx, relayMux, msg); err != nil {
					fail.Add(1)
				} else {
					ok.Add(1)
				}
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)

		cancel()
		muxCleanup()

		errRate := float64(fail.Load()) / float64(ok.Load()+fail.Load()) * 100
		fmt.Fprintf(os.Stderr, "[stress] %d streams: %d ok, %d fail (%.1f%%), %v\n",
			n, ok.Load(), fail.Load(), errRate, elapsed.Round(time.Millisecond))

		if errRate > 5 {
			fmt.Fprintf(os.Stderr, "[stress] breaking point: ~%d streams (>5%% error rate)\n\n", n-500)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "[stress] no breaking point found up to 5000 streams\n\n")
}

func runThroughput() {
	if remoteAddr != "" {
		fmt.Fprintf(os.Stderr, "[throughput] skipped in remote mode (requires mux-level stream access)\n\n")
		return
	}

	const totalBytes = 100 * 1024 * 1024
	const chunkSize = 32 * 1024

	fmt.Fprintf(os.Stderr, "[throughput] %d MB through single stream (%d KB chunks)\n",
		totalBytes/1024/1024, chunkSize/1024)

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()
	relayMux, agentMux, muxCleanup := newMuxPair(10)
	defer muxCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	startAgentForwarder(ctx, agentMux, echoLn.Addr().String())

	stream, err := relayMux.OpenStream(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[throughput] FAIL: open stream: %v\n\n", err)
		return
	}

	chunk := makePayload(chunkSize)
	sent := 0

	readDone := make(chan int64, 1)
	go func() {
		var total int64
		buf := make([]byte, chunkSize)
		for total < totalBytes {
			n, readErr := stream.Read(buf)
			if readErr != nil {
				break
			}
			total += int64(n)
		}
		readDone <- total
	}()

	start := time.Now()
	for sent < totalBytes {
		n, wErr := stream.Write(chunk)
		if wErr != nil {
			break
		}
		sent += n
	}

	received := <-readDone
	elapsed := time.Since(start)

	mbps := float64(received) / elapsed.Seconds() / 1024 / 1024
	fmt.Fprintf(os.Stderr, "[throughput] sent: %d MB, received: %d MB, time: %v\n",
		sent/1024/1024, received/1024/1024, elapsed.Round(time.Millisecond))
	fmt.Fprintf(os.Stderr, "[throughput] rate: %.1f MB/sec\n\n", mbps)
}

func runLatency() {
	fmt.Fprintf(os.Stderr, "[latency] measuring p50/p95/p99 at various concurrency levels\n")

	levels := []int{1, 10, 50, 100, 500}

	if remoteAddr != "" {
		runRemoteLatency(levels)
		return
	}

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()

	msg := makePayload(256)

	fmt.Fprintf(os.Stderr, "%-12s %10s %10s %10s %10s\n",
		"concurrency", "p50", "p95", "p99", "max")

	for _, n := range levels {
		relayMux, agentMux, muxCleanup := newMuxPair(n + 50)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		startAgentForwarder(ctx, agentMux, echoLn.Addr().String())

		latencies := collectLatencies(n, func() error {
			return echoRoundTrip(ctx, relayMux, msg)
		})

		cancel()
		muxCleanup()

		printLatencyRow(n, latencies)
	}
	fmt.Fprintf(os.Stderr, "\n")
}

func runRemoteLatency(levels []int) {
	msg := makePayload(256)

	fmt.Fprintf(os.Stderr, "%-12s %10s %10s %10s %10s\n",
		"concurrency", "p50", "p95", "p99", "max")

	for _, n := range levels {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		latencies := collectLatencies(n, func() error {
			return remoteTCPRoundTrip(ctx, remoteAddr, msg)
		})

		cancel()
		printLatencyRow(n, latencies)
	}
	fmt.Fprintf(os.Stderr, "\n")
}

func collectLatencies(n int, roundTrip func() error) []float64 {
	var wg sync.WaitGroup
	var mu sync.Mutex
	latencies := make([]float64, 0, n)

	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := time.Now()
			if err := roundTrip(); err != nil {
				return
			}
			lat := time.Since(t0).Seconds() * 1000
			mu.Lock()
			latencies = append(latencies, lat)
			mu.Unlock()
		}()
	}
	wg.Wait()

	sort.Float64s(latencies)
	return latencies
}

func printLatencyRow(n int, latencies []float64) {
	fmt.Fprintf(os.Stderr, "%-12d %9.1fms %9.1fms %9.1fms %9.1fms\n",
		n,
		percentile(latencies, 50),
		percentile(latencies, 95),
		percentile(latencies, 99),
		percentile(latencies, 100))
}

func runChurn() {
	if remoteAddr != "" {
		fmt.Fprintf(os.Stderr, "[churn] skipped in remote mode (requires mux-level stream access)\n\n")
		return
	}

	const cycles = 5000

	fmt.Fprintf(os.Stderr, "[churn] %d rapid open/close cycles\n", cycles)

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()
	relayMux, agentMux, muxCleanup := newMuxPair(100)
	defer muxCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	startAgentForwarder(ctx, agentMux, echoLn.Addr().String())

	msg := makePayload(64)
	var ok, fail atomic.Int64

	start := time.Now()
	for range cycles {
		if err := echoRoundTrip(ctx, relayMux, msg); err != nil {
			fail.Add(1)
		} else {
			ok.Add(1)
		}
	}
	elapsed := time.Since(start)

	fmt.Fprintf(os.Stderr, "[churn] %d ok, %d fail, %v (%.0f cycles/sec)\n",
		ok.Load(), fail.Load(), elapsed.Round(time.Millisecond),
		float64(ok.Load())/elapsed.Seconds())
	fmt.Fprintf(os.Stderr, "[churn] active streams after churn: %d\n\n", relayMux.NumStreams())
}

func runRamp() {
	fmt.Fprintf(os.Stderr, "[ramp] gradual concurrency increase\n")

	var levels []int

	if remoteAddr != "" {
		// Smaller levels for remote to avoid overwhelming the relay
		levels = []int{10, 25, 50, 100, 200, 500}
		runRemoteRamp(levels)
		return
	}

	levels = []int{10, 25, 50, 100, 250, 500, 1000, 1500, 2000}

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()

	msg := makePayload(512)

	fmt.Fprintf(os.Stderr, "%-12s %12s %8s %12s\n",
		"concurrency", "streams/sec", "errors", "avg latency")

	for _, n := range levels {
		relayMux, agentMux, muxCleanup := newMuxPair(n + 200)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		startAgentForwarder(ctx, agentMux, echoLn.Addr().String())

		okN, failN, avgLat := runConcurrent(n, func() error {
			return echoRoundTrip(ctx, relayMux, msg)
		})

		cancel()
		muxCleanup()

		printRampRow(n, okN, failN, avgLat)
	}
	fmt.Fprintf(os.Stderr, "\n")
}

func runRemoteRamp(levels []int) {
	msg := makePayload(512)

	fmt.Fprintf(os.Stderr, "%-12s %12s %8s %12s\n",
		"concurrency", "reqs/sec", "errors", "avg latency")

	for _, n := range levels {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		okN, failN, avgLat := runConcurrent(n, func() error {
			return remoteTCPRoundTrip(ctx, remoteAddr, msg)
		})

		cancel()
		printRampRow(n, okN, failN, avgLat)
	}
	fmt.Fprintf(os.Stderr, "\n")
}

func runConcurrent(n int, roundTrip func() error) (ok, fail int64, avgLat time.Duration) {
	var wg sync.WaitGroup
	var okA, failA atomic.Int64
	var totalLat atomic.Int64

	start := time.Now()
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := time.Now()
			if err := roundTrip(); err != nil {
				failA.Add(1)
			} else {
				okA.Add(1)
				totalLat.Add(time.Since(t0).Microseconds())
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	okN := okA.Load()
	if okN > 0 {
		avgLat = time.Duration(totalLat.Load()/okN) * time.Microsecond
	}
	_ = elapsed
	return okN, failA.Load(), avgLat
}

func printRampRow(n int, ok, fail int64, avgLat time.Duration) {
	fmt.Fprintf(os.Stderr, "%-12d %11d ok %7d %11v\n",
		n, ok, fail, avgLat.Round(time.Microsecond))
}

// --- Helpers ---

func echoRoundTrip(ctx context.Context, mux *protocol.MuxSession, msg []byte) error {
	s, err := mux.OpenStream(ctx)
	if err != nil {
		return err
	}
	defer s.Close()

	if _, err := s.Write(msg); err != nil {
		return err
	}

	buf := make([]byte, len(msg))
	total := 0
	for total < len(msg) {
		n, readErr := s.Read(buf[total:])
		if readErr != nil {
			return readErr
		}
		total += n
	}
	return nil
}

func printResult(name string, ok, fail int64, elapsed time.Duration) {
	total := ok + fail
	errRate := float64(fail) / float64(total) * 100

	fmt.Fprintf(os.Stderr, "[%s] %d total, %d ok, %d fail (%.1f%%)\n", name, total, ok, fail, errRate)
	fmt.Fprintf(os.Stderr, "[%s] duration: %v, throughput: %.0f streams/sec\n",
		name, elapsed.Round(time.Millisecond), float64(ok)/elapsed.Seconds())

	if errRate > 1 {
		fmt.Fprintf(os.Stderr, "[%s] FAIL: error rate > 1%%\n\n", name)
	} else {
		fmt.Fprintf(os.Stderr, "[%s] PASS\n\n", name)
	}
}
