package protocol

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each side owns one ID parity (relay odd, agent even). A freed peer ID
// must never be recycled for a locally opened stream, otherwise the two
// sides can pick the same ID at the same time.
func TestMuxSession_FreedPeerIDsAreNotRecycled(t *testing.T) {
	relay, agent := newMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Relay opens and fully closes a stream; the agent frees the odd ID.
	rs, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	as, err := agent.AcceptStream(ctx)
	require.NoError(t, err)
	oddID := rs.ID()
	require.Equal(t, uint32(1), oddID%2)
	require.NoError(t, rs.Close())
	require.NoError(t, as.Close())
	require.Eventually(t, func() bool { return agent.NumStreams() == 0 && relay.NumStreams() == 0 },
		2*time.Second, 10*time.Millisecond)

	// Agent now opens its own stream: it must get an even ID.
	s, err := agent.OpenStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), s.ID()%2, "agent reused a relay-parity ID %d", s.ID())
	assert.NotEqual(t, oddID, s.ID())
}

// A STREAM_OPEN for an ID that is already active must not replace the
// existing session and must not disturb it on either side: the duplicate
// is dropped and the live stream keeps passing data both ways.
func TestMuxSession_DuplicateOpenDoesNotReplaceActiveStream(t *testing.T) {
	relay, agent := newMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rs, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	as, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Inject a duplicate open for the same ID as if the peer reused it.
	agent.handleFrame(&Frame{Version: ProtocolVersion, Command: CmdStreamOpen, StreamID: rs.ID()})

	// The original stream must still be the one in the map and still pass
	// data in both directions.
	_, err = rs.Write([]byte("still alive"))
	require.NoError(t, err)
	buf := make([]byte, 32)
	n, err := as.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "still alive", string(buf[:n]))
	_, err = as.Write([]byte("and back"))
	require.NoError(t, err)
	n, err = rs.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "and back", string(buf[:n]))
	assert.Equal(t, 1, agent.NumStreams())
	assert.Equal(t, 1, relay.NumStreams())
}
