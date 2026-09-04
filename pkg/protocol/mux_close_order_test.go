package protocol

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A graceful Close issued right after Write must not overtake the data:
// the peer has to receive every byte and only then see EOF.
func TestMuxSession_CloseAfterWritePreservesOrder(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	const chunks = 32
	chunk := []byte("0123456789abcdef")
	go func() {
		for i := 0; i < chunks; i++ {
			if _, wErr := agentStream.Write(chunk); wErr != nil {
				return
			}
		}
		agentStream.Close() //nolint:errcheck // test writer
	}()

	got := make([]byte, 0, chunks*len(chunk))
	buf := make([]byte, 64)
	for {
		n, rErr := relayStream.Read(buf)
		got = append(got, buf[:n]...)
		if rErr == io.EOF {
			break
		}
		require.NoError(t, rErr)
	}
	assert.Len(t, got, chunks*len(chunk), "data lost: FIN overtook STREAM_DATA")
}

// Closing the last open half (remote already sent FIN) must still flush
// buffered writes before the stream is torn down.
func TestMuxSession_FullCloseFlushesBufferedWrites(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Relay half-closes first, agent replies then closes.
	require.NoError(t, relayStream.Close())
	_, err = agentStream.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)

	reply := []byte("late reply after remote FIN")
	_, err = agentStream.Write(reply)
	require.NoError(t, err)
	require.NoError(t, agentStream.Close())

	got := make([]byte, 0, len(reply))
	buf := make([]byte, 64)
	for {
		n, rErr := relayStream.Read(buf)
		got = append(got, buf[:n]...)
		if rErr == io.EOF {
			break
		}
		require.NoError(t, rErr)
	}
	assert.Equal(t, string(reply), string(got))
}
