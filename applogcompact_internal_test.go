package via

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// recvOffset reads one record off a subscription within a timeout, returning its
// offset — so a test can assert WHICH offset a compacted log resumes at.
func recvOffset(t *testing.T, sub <-chan Record) Offset {
	t.Helper()
	select {
	case r := <-sub:
		return r.Offset
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a record")
		return 0
	}
}

// lowestRetainedOffset subscribes from genesis and returns the FIRST delivered
// record's offset — the lowest offset the log still holds after any compaction
// (0 if the log delivered nothing within the window).
func lowestRetainedOffset(t *testing.T, bp Backplane, key string) Offset {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := bp.Subscribe(ctx, key, 0)
	if err != nil {
		// A torn-down backplane (Close during a require.Never/Eventually poll,
		// whose check fn runs in a straggler goroutine that can outlive the test
		// body's deferred Close) retains nothing — report 0 rather than failing
		// the test from inside the polled goroutine.
		return 0
	}
	select {
	case r, ok := <-sub:
		if !ok {
			return 0
		}
		return r.Offset
	case <-time.After(time.Second):
		return 0
	}
}

// nonCompactingBackplane embeds the Backplane interface but NOT Compact, so the
// runtime's type-assert to Compactor fails — modelling a backend that declines
// compaction and must still snapshot.
type nonCompactingBackplane struct{ Backplane }

// Compaction reclaims the prefix a snapshot already covers, but retained records
// MUST keep their original offsets — a resuming pod that passed offset N must
// still resume exactly after N. Re-numbering the survivors to 1 would silently
// re-deliver or skip events.
func TestCompactDropsPrefixButKeepsRetainedOffsetsStable(t *testing.T) {
	b := InMemory()
	defer b.Close()
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		off, err := b.Append(ctx, "k", []byte{byte(i)})
		require.NoError(t, err)
		require.Equal(t, Offset(i), off)
	}
	c, ok := b.(Compactor)
	require.True(t, ok, "the in-memory backplane must offer compaction")
	require.NoError(t, c.Compact(ctx, "k", 4)) // discard offsets < 4 (1,2,3)

	// Head is unchanged — compaction reclaims the prefix, never the head.
	head, _, err := b.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, Offset(5), head)

	// A genesis subscriber resumes at the lowest RETAINED offset, gap-free, with
	// offsets UNCHANGED (4, 5) — never renumbered to 1, 2.
	sub, err := b.Subscribe(ctx, "k", 0)
	require.NoError(t, err)
	require.Equal(t, Offset(4), recvOffset(t, sub))
	require.Equal(t, Offset(5), recvOffset(t, sub))
}

// A re-issued, stale, or over-large beforeOffset must never corrupt the log or
// move the head — compaction is idempotent and clamped to committed offsets.
func TestCompactIsIdempotentAndClampedToCommitted(t *testing.T) {
	b := InMemory()
	defer b.Close()
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		_, err := b.Append(ctx, "k", []byte{byte(i)})
		require.NoError(t, err)
	}
	c := b.(Compactor)
	require.NoError(t, c.Compact(ctx, "k", 0))   // before:0 (first-snapshot floor) → no-op
	require.NoError(t, c.Compact(ctx, "k", 2))   // drop offset 1
	require.NoError(t, c.Compact(ctx, "k", 2))   // again → no-op
	require.NoError(t, c.Compact(ctx, "k", 1))   // below the floor → no-op
	require.NoError(t, c.Compact(ctx, "k", 999)) // beyond head → clamp to committed

	head, _, err := b.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, Offset(3), head, "head must never move under compaction")
}

// Offsets are a monotone resume primitive: an append AFTER compaction must
// continue from the head, never restart base-relative. A renumbered offset would
// let a resuming pod silently re-process or skip events.
func TestAppendAfterCompactContinuesMonotoneOffsets(t *testing.T) {
	b := InMemory()
	defer b.Close()
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		_, err := b.Append(ctx, "k", []byte{byte(i)})
		require.NoError(t, err)
	}
	require.NoError(t, b.(Compactor).Compact(ctx, "k", 4)) // drop 1..3, retain 4,5

	off, err := b.Append(ctx, "k", []byte{6})
	require.NoError(t, err)
	require.Equal(t, Offset(6), off, "append after compaction must continue from the head, not restart")

	sub, err := b.Subscribe(ctx, "k", 0)
	require.NoError(t, err)
	require.Equal(t, Offset(4), recvOffset(t, sub))
	require.Equal(t, Offset(5), recvOffset(t, sub))
	require.Equal(t, Offset(6), recvOffset(t, sub))
}

// Compaction is monotone: the retained floor only ever rises. A stale or
// out-of-order beforeOffset must not move the floor backward (which would
// "resurrect" discarded offsets) or move the head.
func TestSequentialCompactionsAdvanceMonotonically(t *testing.T) {
	b := InMemory()
	defer b.Close()
	ctx := context.Background()
	for i := 1; i <= 10; i++ {
		_, err := b.Append(ctx, "k", []byte{byte(i)})
		require.NoError(t, err)
	}
	c := b.(Compactor)
	require.NoError(t, c.Compact(ctx, "k", 4))
	require.Equal(t, Offset(4), lowestRetainedOffset(t, b, "k"))
	require.NoError(t, c.Compact(ctx, "k", 7))
	require.Equal(t, Offset(7), lowestRetainedOffset(t, b, "k"))
	require.NoError(t, c.Compact(ctx, "k", 5)) // backward → no-op
	require.Equal(t, Offset(7), lowestRetainedOffset(t, b, "k"), "the floor must never move backward")

	head, _, err := b.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, Offset(10), head, "head is invariant under compaction")
}

// The projector reclaims storage automatically, but compaction must trail a
// DURABLE snapshot (snapshot-FIRST, compact-SECOND) — it must never discard at
// or beyond the covered offset, or a cold start could not resume.
func TestProjectorAutoCompactsTrailingTheDurableSnapshot(t *testing.T) {
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}

	// Compaction ran: the lowest retained offset rose above genesis.
	require.Eventually(t, func() bool {
		return lowestRetainedOffset(t, app.backplane, "k") > 1
	}, 2*time.Second, 10*time.Millisecond, "the projector must compact the prefix")

	head, _, err := app.backplane.Head(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, Offset(5), head)

	// The durable snapshot's covered offset is never compacted away (the floor
	// lags one generation), so a cold start can always resume from it.
	data, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
	require.True(t, ok)
	var cp checkpoint
	require.NoError(t, json.Unmarshal(data, &cp))
	require.LessOrEqual(t, lowestRetainedOffset(t, app.backplane, "k"), cp.CoveredOffset,
		"compaction must never pass the durable snapshot's covered offset")
}

// The payoff: a fresh pod sharing a backplane whose prefix has been compacted
// still cold-starts to the full projection — it seeds from the snapshot and
// never needs the discarded events.
func TestFreshProjectorColdStartsAfterPrefixCompacted(t *testing.T) {
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	var hA StateAppEvents[envEv, []int]
	hA.bindWireKey("k")
	hA.bindApp(app)
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		return lowestRetainedOffset(t, app.backplane, "k") > 1
	}, 2*time.Second, 10*time.Millisecond, "the prefix must be compacted before the fresh pod starts")

	appB := New(WithTestServer(&server), WithBackplane(app.backplane))
	var hB StateAppEvents[envEv, []int]
	hB.bindWireKey("k")
	hB.bindApp(appB)

	require.Eventually(t, func() bool {
		p := projection(appB, "k")
		return len(p) == 5 && p[0] == 1 && p[4] == 5
	}, 2*time.Second, 10*time.Millisecond,
		"the fresh pod must reach the full projection from the snapshot despite compaction")
}

// A backend that declines Compactor must still snapshot — the runtime falls back
// to snapshot-only and never panics or wedges, leaving the prefix intact.
func TestSnapshotOnlyWhenBackendDeclinesCompaction(t *testing.T) {
	var server *httptest.Server
	bp := nonCompactingBackplane{InMemory()}
	app := New(WithTestServer(&server), WithBackplane(bp), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		_, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
		return ok
	}, 2*time.Second, 10*time.Millisecond, "snapshot must still be written")
	require.Equal(t, Offset(1), lowestRetainedOffset(t, app.backplane, "k"),
		"a backend without Compactor leaves the prefix intact")
}
