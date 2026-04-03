package protocol

import (
	"bytes"
	"encoding/binary"
	"sync"
	"testing"
)

// BenchmarkDecodeFrameWithPool compares decode with and without sync.Pool.
func BenchmarkDecodeFrameWithPool(b *testing.B) {
	codec := NewFrameCodec()

	// Build a valid frame to decode repeatedly
	var frameBuf bytes.Buffer
	testFrame := &Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		StreamID: 42,
		Payload:  make([]byte, 4096),
	}
	if err := codec.WriteFrame(&frameBuf, testFrame); err != nil {
		b.Fatal(err)
	}
	frameBytes := frameBuf.Bytes()

	b.Run("no-pool", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			r := bytes.NewReader(frameBytes)
			f, err := codec.ReadFrame(r)
			if err != nil {
				b.Fatal(err)
			}
			_ = f
		}
	})

	// Simulate pool usage: pool the payload buffer
	payloadPool := sync.Pool{
		New: func() any {
			buf := make([]byte, 4096)
			return &buf
		},
	}

	b.Run("with-pool", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			r := bytes.NewReader(frameBytes)

			// Read header manually
			var hdr [HeaderSize]byte
			if _, err := r.Read(hdr[:]); err != nil {
				b.Fatal(err)
			}

			payloadLen := binary.BigEndian.Uint32(hdr[8:12])

			// Get payload buffer from pool
			bufPtr := payloadPool.Get().(*[]byte)
			buf := *bufPtr
			if int(payloadLen) > len(buf) {
				buf = make([]byte, payloadLen)
			}
			payload := buf[:payloadLen]
			if _, err := r.Read(payload); err != nil {
				b.Fatal(err)
			}

			_ = payload
			*bufPtr = buf
			payloadPool.Put(bufPtr)
		}
	})
}
