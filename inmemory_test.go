package via_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recv reads one Record from ch with a timeout so a buggy Subscribe can never
// hang the suite. closed reports whether the channel was closed.
func recv(t *testing.T, ch <-chan via.Record) (rec via.Record, closed bool) {
	t.Helper()
	select {
	case r, ok := <-ch:
		return r, !ok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a Record")
		return via.Record{}, false
	}
}

// expectQuiet asserts that NOTHING arrives on ch for a short window — used to
// prove the live tail genuinely blocks on a real append rather than pre-
// buffering (or hardcoding) records a correct stream must not have yet.
func expectQuiet(t *testing.T, ch <-chan via.Record) {
	t.Helper()
	select {
	case r, ok := <-ch:
		t.Fatalf("expected no record yet, got %+v (closed=%v)", r, !ok)
	case <-time.After(150 * time.Millisecond):
	}
}

// A pod resumes from its last-applied offset and must never miss or
// double-count a record — so Append must hand out a per-key offset that only
// ever increases, starting at 1, and Head must report the high-water mark.
func TestAppendAssignsMonotonePerKeyOffsetAndHeadReportsIt(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	defer bp.Close()
	ctx := context.Background()

	off, epoch, err := bp.Head(ctx, "room")
	require.NoError(t, err)
	assert.Equal(t, via.Offset(0), off, "an empty key's head offset is 0 (before the first record)")
	_ = epoch

	o1, err := bp.Append(ctx, "room", []byte("a"))
	require.NoError(t, err)
	o2, err := bp.Append(ctx, "room", []byte("b"))
	require.NoError(t, err)
	assert.Equal(t, via.Offset(1), o1, "first append on a key is offset 1")
	assert.Equal(t, via.Offset(2), o2, "offsets increase by one")

	head, _, err := bp.Head(ctx, "room")
	require.NoError(t, err)
	assert.Equal(t, via.Offset(2), head, "Head reports the highest committed offset")
}

// Reconnect-rehydrate (#7) and stranding (#3) rest entirely on this: a
// subscriber that passes its last-applied offset K gets EVERY record after K,
// in order, and then keeps receiving live appends. If replay skipped or
// reordered, a reconnecting pod would silently diverge.
func TestSubscribeReplaysAfterOffsetThenLiveTails(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, v := range []string{"a", "b", "c"} {
		_, err := bp.Append(ctx, "room", []byte(v))
		require.NoError(t, err)
	}

	// Resume from offset 1: must see only b(2), c(3), then a live d(4).
	ch, err := bp.Subscribe(ctx, "room", via.Offset(1))
	require.NoError(t, err)

	r, _ := recv(t, ch)
	require.Equal(t, via.Offset(2), r.Offset, "resume from 1 skips offset 1")
	require.Equal(t, []byte("b"), r.Data)
	r, _ = recv(t, ch)
	require.Equal(t, via.Offset(3), r.Offset)
	require.Equal(t, []byte("c"), r.Data)

	// The tail must BLOCK here: with no append since offset 3, a correct stream
	// has nothing to deliver. A pre-buffered or hardcoded stub fails this.
	expectQuiet(t, ch)

	// Live tail: only now, after a real append, does offset 4 arrive.
	_, err = bp.Append(ctx, "room", []byte("d"))
	require.NoError(t, err)
	r, _ = recv(t, ch)
	require.Equal(t, via.Offset(4), r.Offset)
	require.Equal(t, []byte("d"), r.Data)

	// A fresh subscription from genesis must replay the FULL dense sequence in
	// order — proving `from` is honored against real data, not a constant.
	full, err := bp.Subscribe(ctx, "room", via.Offset(0))
	require.NoError(t, err)
	for i, want := range []string{"a", "b", "c", "d"} {
		r, _ = recv(t, full)
		require.Equal(t, via.Offset(i+1), r.Offset, "genesis replay is in dense offset order")
		require.Equal(t, []byte(want), r.Data)
	}
}

// The runtime hands Append the encoded event bytes and is free to reuse or
// mutate that buffer afterwards. The log must own a private copy, or a later
// mutation would silently rewrite history every subscriber replays.
func TestAppendOwnsACopyOfTheRecordBytes(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	defer bp.Close()
	ctx := context.Background()

	buf := []byte("original")
	_, err := bp.Append(ctx, "room", buf)
	require.NoError(t, err)
	for i := range buf {
		buf[i] = 'X' // caller scribbles over its buffer after Append
	}

	ch, err := bp.Subscribe(ctx, "room", via.Offset(0))
	require.NoError(t, err)
	r, _ := recv(t, ch)
	assert.Equal(t, []byte("original"), r.Data, "the log must not alias the caller's mutated buffer")
}

// Close runs on App.Shutdown and may be reached more than once (defer + explicit
// shutdown); a second Close must be a no-op, never a panic on an already-closed
// channel.
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	require.NoError(t, bp.Close())
	assert.NoError(t, bp.Close(), "a second Close is a harmless no-op")
}

// Head reports the per-key Epoch alongside the offset; the in-memory backplane
// never resets its offset space, so it stays at the genesis generation.
func TestHeadReportsGenesisEpoch(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	defer bp.Close()
	ctx := context.Background()

	_, err := bp.Append(ctx, "room", []byte("a"))
	require.NoError(t, err)
	_, epoch, err := bp.Head(ctx, "room")
	require.NoError(t, err)
	assert.Equal(t, via.Epoch(0), epoch, "the in-memory log stays at the genesis epoch")
}

// A subscriber whose ctx is cancelled must unwind promptly (its channel
// closes), so a disconnected tab's projector goroutine cannot leak.
func TestSubscribeUnwindsOnContextCancel(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())

	_, err := bp.Append(ctx, "room", []byte("a"))
	require.NoError(t, err)
	ch, err := bp.Subscribe(ctx, "room", via.Offset(0))
	require.NoError(t, err)
	r, _ := recv(t, ch)
	require.Equal(t, via.Offset(1), r.Offset)

	cancel()
	_, closed := recv(t, ch)
	assert.True(t, closed, "cancelling the ctx closes the subscriber channel")
}

// Distinct StateAppEvents fields are independent aggregates: the design
// guarantees NO cross-key ordering, so interleaving appends across keys must
// leave each key with its own dense 1..N sequence.
func TestPerKeyStreamsAreIndependent(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	defer bp.Close()
	ctx := context.Background()

	a1, _ := bp.Append(ctx, "alpha", []byte("a1"))
	b1, _ := bp.Append(ctx, "beta", []byte("b1"))
	a2, _ := bp.Append(ctx, "alpha", []byte("a2"))
	b2, _ := bp.Append(ctx, "beta", []byte("b2"))

	assert.Equal(t, via.Offset(1), a1)
	assert.Equal(t, via.Offset(2), a2)
	assert.Equal(t, via.Offset(1), b1, "beta's offsets are independent of alpha's")
	assert.Equal(t, via.Offset(2), b2)

	headA, _, _ := bp.Head(ctx, "alpha")
	headB, _, _ := bp.Head(ctx, "beta")
	assert.Equal(t, via.Offset(2), headA)
	assert.Equal(t, via.Offset(2), headB)
}

// Value-shaped StateApp survivability rests on CAS: a write only lands if the
// caller saw the current revision, so two racing writers can't silently clobber
// each other — the loser gets ErrCASConflict and must reload.
func TestCASRejectsStaleRevisionAndPreservesValue(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	defer bp.Close()
	ctx := context.Background()

	_, _, ok, err := bp.LoadSnapshot(ctx, "cell")
	require.NoError(t, err)
	assert.False(t, ok, "an unwritten cell does not exist")

	rev1, err := bp.CAS(ctx, "cell", via.Rev(0), []byte("first"))
	require.NoError(t, err)
	assert.Equal(t, via.Rev(1), rev1, "first write (expectedRev 0 = must-not-exist) yields rev 1")

	// A writer holding the stale rev 0 must lose.
	_, err = bp.CAS(ctx, "cell", via.Rev(0), []byte("clobber"))
	assert.ErrorIs(t, err, via.ErrCASConflict)

	data, rev, ok, err := bp.LoadSnapshot(ctx, "cell")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []byte("first"), data, "a rejected CAS leaves the cell unchanged")
	assert.Equal(t, via.Rev(1), rev)

	// A writer with the current rev succeeds and advances the rev.
	rev2, err := bp.CAS(ctx, "cell", rev1, []byte("second"))
	require.NoError(t, err)
	assert.Equal(t, via.Rev(2), rev2)
}

// Graceful drain on App.Shutdown: after Close the backplane must refuse new
// appends and never block, and live subscribers' channels must close so their
// projector goroutines unwind instead of leaking.
func TestClosedBackplaneRefusesAppendAndClosesSubscribers(t *testing.T) {
	t.Parallel()
	bp := via.InMemory()
	ctx := context.Background()

	_, err := bp.Append(ctx, "room", []byte("a"))
	require.NoError(t, err)

	ch, err := bp.Subscribe(ctx, "room", via.Offset(0))
	require.NoError(t, err)
	// Drain the replayed record so the next recv observes the close.
	r, _ := recv(t, ch)
	assert.Equal(t, via.Offset(1), r.Offset)

	require.NoError(t, bp.Close())

	_, err = bp.Append(ctx, "room", []byte("b"))
	assert.ErrorIs(t, err, via.ErrClosed, "Append after Close returns ErrClosed")

	_, closed := recv(t, ch)
	assert.True(t, closed, "Close closes live subscriber channels")

	// Subscribe after Close also reports ErrClosed.
	_, err = bp.Subscribe(ctx, "room", via.Offset(0))
	assert.True(t, errors.Is(err, via.ErrClosed), "Subscribe after Close returns ErrClosed")
}
