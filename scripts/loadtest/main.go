// loadtest is a comprehensive performance test suite for atlax.
//
// It runs in-process using the actual protocol library -- no external
// relay or agent needed. A relay MuxSession and agent MuxSession are
// connected via net.Pipe, and an echo server simulates the local service.
//
// Usage:
//
//	go run ./scripts/loadtest/                         # run all benchmarks
//	go run ./scripts/loadtest/ -bench load             # run only the load test
//	go run ./scripts/loadtest/ -bench stress           # run only the stress test
//	go run ./scripts/loadtest/ -bench throughput       # run only the throughput test
//	go run ./scripts/loadtest/ -bench latency          # run only the latency test
//	go run ./scripts/loadtest/ -bench churn            # run only the churn test
//	go run ./scripts/loadtest/ -bench ramp             # run only the ramp test
//	go run -race ./scripts/loadtest/ -bench load       # with race detector

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

func main() {
	bench := flag.String("bench", "all", "benchmark to run: all, load, stress, throughput, latency, churn, ramp")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "atlax performance test suite\n")
	fmt.Fprintf(os.Stderr, "============================\n\n")

	switch *bench {
	case "all":
		runLoad()
		runStress()
		runThroughput()
		runLatency()
		runChurn()
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

func startEchoServer() (net.Listener, func()) {
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
				io.Copy(conn, conn)
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
				go func() { io.Copy(local, s); done <- struct{}{} }()
				go func() { io.Copy(s, local); done <- struct{}{} }()
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

// --- Benchmarks ---

// runLoad: sustained concurrent streams at target capacity.
// Target: 1000 concurrent streams, 1KB messages, <1% error rate.
func runLoad() {
	const streams = 1000
	const msgSize = 1024

	fmt.Fprintf(os.Stderr, "[load] %d concurrent streams, %d bytes each\n", streams, msgSize)

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

	printResult("load", ok.Load(), fail.Load(), elapsed, 0, nil)
}

// runStress: push beyond limits to find the breaking point.
// Opens streams in batches of 500 until errors exceed 5%.
func runStress() {
	fmt.Fprintf(os.Stderr, "[stress] finding breaking point (batches of 500)\n")

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()

	msg := makePayload(512)
	batchSize := 500

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
			fmt.Fprintf(os.Stderr, "[stress] breaking point: ~%d streams (>5%% error rate)\n\n", n-batchSize)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "[stress] no breaking point found up to 5000 streams\n\n")
}

// runThroughput: max bytes/sec through the tunnel with a single stream.
// Sends 100MB through one stream and measures transfer rate.
func runThroughput() {
	const totalBytes = 100 * 1024 * 1024 // 100MB
	const chunkSize = 32 * 1024          // 32KB chunks

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

	// Read in background
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

// runLatency: per-stream latency at various concurrency levels.
// Measures p50, p95, p99 latency at 1, 10, 50, 100, 500 concurrent streams.
func runLatency() {
	fmt.Fprintf(os.Stderr, "[latency] measuring p50/p95/p99 at various concurrency levels\n")

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()

	msg := makePayload(256)
	levels := []int{1, 10, 50, 100, 500}

	fmt.Fprintf(os.Stderr, "%-12s %10s %10s %10s %10s\n",
		"concurrency", "p50", "p95", "p99", "max")

	for _, n := range levels {
		relayMux, agentMux, muxCleanup := newMuxPair(n + 50)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		startAgentForwarder(ctx, agentMux, echoLn.Addr().String())

		var wg sync.WaitGroup
		var mu sync.Mutex
		latencies := make([]float64, 0, n)

		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t0 := time.Now()
				if err := echoRoundTrip(ctx, relayMux, msg); err != nil {
					return
				}
				lat := time.Since(t0).Seconds() * 1000 // ms
				mu.Lock()
				latencies = append(latencies, lat)
				mu.Unlock()
			}()
		}
		wg.Wait()
		cancel()
		muxCleanup()

		sort.Float64s(latencies)
		fmt.Fprintf(os.Stderr, "%-12d %9.1fms %9.1fms %9.1fms %9.1fms\n",
			n,
			percentile(latencies, 50),
			percentile(latencies, 95),
			percentile(latencies, 99),
			percentile(latencies, 100))
	}
	fmt.Fprintf(os.Stderr, "\n")
}

// runChurn: rapid open/close cycles. Tests stream ID recycling
// and resource cleanup under high churn.
func runChurn() {
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
	// Serial to test recycling under controlled conditions
	for range cycles {
		if err := echoRoundTrip(ctx, relayMux, msg); err != nil {
			fail.Add(1)
		} else {
			ok.Add(1)
		}
	}
	elapsed := time.Since(start)

	relayMux.NumStreams() // force read
	fmt.Fprintf(os.Stderr, "[churn] %d ok, %d fail, %v (%.0f cycles/sec)\n",
		ok.Load(), fail.Load(), elapsed.Round(time.Millisecond),
		float64(ok.Load())/elapsed.Seconds())
	fmt.Fprintf(os.Stderr, "[churn] active streams after churn: %d\n\n", relayMux.NumStreams())
}

// runRamp: gradually increase concurrency, measure throughput curve.
// Shows how performance degrades as load increases.
func runRamp() {
	fmt.Fprintf(os.Stderr, "[ramp] gradual concurrency increase\n")

	echoLn, echoCleanup := startEchoServer()
	defer echoCleanup()

	msg := makePayload(512)
	levels := []int{10, 25, 50, 100, 250, 500, 1000, 1500, 2000}

	fmt.Fprintf(os.Stderr, "%-12s %12s %8s %12s\n",
		"concurrency", "streams/sec", "errors", "avg latency")

	for _, n := range levels {
		relayMux, agentMux, muxCleanup := newMuxPair(n + 200)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		startAgentForwarder(ctx, agentMux, echoLn.Addr().String())

		var wg sync.WaitGroup
		var ok, fail atomic.Int64
		var totalLat atomic.Int64

		start := time.Now()
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t0 := time.Now()
				if err := echoRoundTrip(ctx, relayMux, msg); err != nil {
					fail.Add(1)
				} else {
					ok.Add(1)
					totalLat.Add(time.Since(t0).Microseconds())
				}
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)
		cancel()
		muxCleanup()

		okN := ok.Load()
		var avgLat time.Duration
		if okN > 0 {
			avgLat = time.Duration(totalLat.Load()/okN) * time.Microsecond
		}

		fmt.Fprintf(os.Stderr, "%-12d %11.0f/s %7d %11v\n",
			n, float64(okN)/elapsed.Seconds(), fail.Load(), avgLat.Round(time.Microsecond))
	}
	fmt.Fprintf(os.Stderr, "\n")
}

// --- Helpers ---

// echoRoundTrip opens a stream, writes msg, reads the echo, closes.
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

func printResult(name string, ok, fail int64, elapsed time.Duration, avgLat time.Duration, latencies []float64) {
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
