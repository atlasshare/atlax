package protocol

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A peer may close a stream immediately after acknowledging it. The FIN
// can then arrive before the opener has consumed the ACK; it must still
// be applied so the opener's first Read reports EOF instead of blocking.
func TestMuxSession_FINRightAfterACKIsNotLost(t *testing.T) {
	relay, agent := newMuxPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Relay-side: accept and close instantly, no data.
	go func() {
		for {
			s, err := relay.AcceptStream(ctx)
			if err != nil {
				return
			}
			s.Close() //nolint:errcheck // test peer
		}
	}()

	s, err := agent.OpenStream(ctx)
	require.NoError(t, err)

	res := make(chan error, 1)
	go func() {
		_, rErr := s.Read(make([]byte, 4))
		res <- rErr
	}()
	select {
	case rErr := <-res:
		require.ErrorIs(t, rErr, io.EOF)
	case <-time.After(2 * time.Second):
		t.Fatal("Read blocked: FIN that arrived before the opener marked the stream Open was dropped")
	}
}
