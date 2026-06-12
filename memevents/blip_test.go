package memevents_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/backplanetest"
	"github.com/go-via/via/memevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlaky_passesBackplaneConformanceWhenQuiet(t *testing.T) {
	t.Parallel()
	backplanetest.RunConformance(t, func() via.Backplane {
		return memevents.NewFlaky(via.InMemory())
	})
}

func TestFlaky_blipSeversLiveSubscriptionsWithoutClosingTheBackplane(t *testing.T) {
	t.Parallel()
	bp := memevents.NewFlaky(via.InMemory())
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := bp.Append(ctx, "k", []byte("pre"))
	require.NoError(t, err)
	ch, err := bp.Subscribe(ctx, "k", 0)
	require.NoError(t, err)
	first := recvWithin(t, ch)
	assert.Equal(t, []byte("pre"), first.Data)

	bp.Blip()
	requireClosedWithin(t, ch)

	// The underlying stream is intact: a re-subscribe from the last-seen
	// offset resumes gap-free and live-tails new appends.
	ch2, err := bp.Subscribe(ctx, "k", first.Offset)
	require.NoError(t, err)
	_, err = bp.Append(ctx, "k", []byte("post"))
	require.NoError(t, err)
	assert.Equal(t, []byte("post"), recvWithin(t, ch2).Data)
}

func TestFlaky_blipSeversEveryLiveSubscription(t *testing.T) {
	t.Parallel()
	bp := memevents.NewFlaky(via.InMemory())
	defer bp.Close()
	ctx := context.Background()

	chA, err := bp.Subscribe(ctx, "a", 0)
	require.NoError(t, err)
	chB, err := bp.Subscribe(ctx, "b", 0)
	require.NoError(t, err)

	bp.Blip()
	requireClosedWithin(t, chA)
	requireClosedWithin(t, chB)
}

func TestFlaky_failSubscribesRejectsExactlyTheNextNCalls(t *testing.T) {
	t.Parallel()
	bp := memevents.NewFlaky(via.InMemory())
	defer bp.Close()
	ctx := context.Background()

	bp.FailSubscribes(2)
	_, err := bp.Subscribe(ctx, "k", 0)
	assert.Error(t, err, "first armed Subscribe must fail")
	_, err = bp.Subscribe(ctx, "k", 0)
	assert.Error(t, err, "second armed Subscribe must fail")

	ch, err := bp.Subscribe(ctx, "k", 0)
	require.NoError(t, err, "the call after the armed failures must succeed")
	_, err = bp.Append(ctx, "k", []byte("a"))
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), recvWithin(t, ch).Data)
}

// requireClosedWithin fails unless ch closes promptly without delivering
// another record — the observable shape of a transient disconnect.
func requireClosedWithin(t *testing.T, ch <-chan via.Record) {
	t.Helper()
	select {
	case rec, ok := <-ch:
		require.False(t, ok, "expected the channel to close, got record %+v", rec)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the channel to close")
	}
}
