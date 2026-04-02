// loadtest is a standalone load test tool for atlax.
// It starts a relay and agent in-process, then opens N concurrent
// client connections sending data through the tunnel to an echo server.
//
// Usage: go run ./scripts/loadtest/ -streams 1000 -size 1024

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

func main() {
	streams := flag.Int("streams", 100, "number of concurrent streams")
	msgSize := flag.Int("size", 1024, "message size in bytes per stream")
	flag.Parse()

	log.SetOutput(os.Stderr)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_ = logger

	fmt.Fprintf(os.Stderr, "load test: %d concurrent streams, %d bytes each\n", *streams, *msgSize)

	// Start echo server
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	defer echoLn.Close()
	go runEchoServer(echoLn)

	// Create relay <-> agent mux pair via net.Pipe
	relayConn, agentConn := net.Pipe()
	cfg := protocol.MuxConfig{
		MaxConcurrentStreams: *streams + 100,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576 * 4,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	}

	relayMux := protocol.NewMuxSession(relayConn, protocol.RoleRelay, cfg)
	agentMux := protocol.NewMuxSession(agentConn, protocol.RoleAgent, cfg)
	defer relayMux.Close()
	defer agentMux.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Agent side: accept streams and forward to echo server
	go func() {
		for {
			stream, acceptErr := agentMux.AcceptStream(ctx)
			if acceptErr != nil {
				return
			}
			go forwardToEcho(ctx, stream, echoLn.Addr().String())
		}
	}()

	// Client side: open N concurrent streams
	msg := make([]byte, *msgSize)
	for i := range len(msg) {
		msg[i] = byte(i % 256)
	}

	var wg sync.WaitGroup
	var succeeded atomic.Int64
	var failed atomic.Int64
	var totalLatency atomic.Int64

	start := time.Now()

	for i := range *streams {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			t0 := time.Now()
			s, openErr := relayMux.OpenStream(ctx)
			if openErr != nil {
				failed.Add(1)
				return
			}
			defer s.Close()

			// Write message
			if _, wErr := s.Write(msg); wErr != nil {
				failed.Add(1)
				return
			}

			// Read echo response
			buf := make([]byte, len(msg))
			total := 0
			for total < len(msg) {
				n, rErr := s.Read(buf[total:])
				if rErr != nil {
					failed.Add(1)
					return
				}
				total += n
			}

			latency := time.Since(t0)
			totalLatency.Add(latency.Microseconds())
			succeeded.Add(1)
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	ok := succeeded.Load()
	fail := failed.Load()
	total := ok + fail

	var avgLatency time.Duration
	if ok > 0 {
		avgLatency = time.Duration(totalLatency.Load()/ok) * time.Microsecond
	}

	fmt.Fprintf(os.Stderr, "\n--- Load Test Results ---\n")
	fmt.Fprintf(os.Stderr, "Streams:     %d total, %d succeeded, %d failed\n", total, ok, fail)
	fmt.Fprintf(os.Stderr, "Duration:    %v\n", elapsed.Round(time.Millisecond))
	fmt.Fprintf(os.Stderr, "Avg latency: %v per stream\n", avgLatency)
	fmt.Fprintf(os.Stderr, "Throughput:  %.0f streams/sec\n", float64(ok)/elapsed.Seconds())
	fmt.Fprintf(os.Stderr, "Error rate:  %.1f%%\n", float64(fail)/float64(total)*100)

	if float64(fail)/float64(total) > 0.01 {
		fmt.Fprintf(os.Stderr, "\nFAIL: error rate > 1%%\n")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\nPASS\n")
}

func runEchoServer(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			io.Copy(conn, conn)
		}()
	}
}

func forwardToEcho(ctx context.Context, stream protocol.Stream, echoAddr string) {
	local, err := net.DialTimeout("tcp", echoAddr, 5*time.Second)
	if err != nil {
		return
	}
	defer local.Close()
	defer stream.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(local, stream); done <- struct{}{} }()
	go func() { io.Copy(stream, local); done <- struct{}{} }()

	select {
	case <-done:
	case <-ctx.Done():
	}
}
