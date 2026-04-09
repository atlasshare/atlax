package main

import (
	"flag"
	"io"
	"log"
	"net"
	"sync/atomic"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9999", "listen address")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("echo server listening on %s", *addr)

	var active atomic.Int64
	var total atomic.Int64

	for {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			log.Printf("accept: %v", acceptErr)
			continue
		}
		active.Add(1)
		total.Add(1)
		go func() {
			defer conn.Close()
			defer active.Add(-1)
			buf := make([]byte, 32*1024)
			if _, copyErr := io.CopyBuffer(conn, conn, buf); copyErr != nil {
				// Client disconnect is normal, not an error
				return
			}
		}()
	}
}
