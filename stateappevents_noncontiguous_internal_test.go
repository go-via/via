package via

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gappedOffsets wraps a Backplane and rewrites each delivered record's offset to
// a NON-CONTIGUOUS sequence (×stride), modelling a backend (e.g. NATS JetStream)
// whose per-key offsets are a GLOBAL stream sequence with gaps once several keys
// share one stream. Offsets stay strictly increasing and nothing is lost — the
// "gaps" are simply other subjects' sequence numbers.
type gappedOffsets struct {
	Backplane
	stride Offset
}

// Precondition: from is always 0 (cold start) or a previously-delivered (×stride)
// offset, so from/stride inverts the rewrite exactly.
func (g gappedOffsets) Subscribe(ctx context.Context, key string, from Offset) (<-chan Record, error) {
	in, err := g.Backplane.Subscribe(ctx, key, from/g.stride)
	if err != nil {
		return nil, err
	}
	out := make(chan Record)
	go func() {
		defer close(out)
		for r := range in {
			r.Offset *= g.stride
			select {
			case out <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// A backend whose per-key offsets are non-contiguous (a shared JetStream stream
// sequenced across subjects) must NOT be mistaken for a compacted log: with no
// compaction — hence no snapshot — every event must still fold. This is the
// viashowcase multi-log bug, where the projector halted on the very first event
// (offset > 1) and silently dropped them all.
func TestProjectorFoldsNonContiguousOffsetsWhenNothingWasCompacted(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(gappedOffsets{Backplane: InMemory(), stride: 3}))
	defer server.Close()
	bindLog(app, "k")
	ctx := context.Background()

	for _, n := range []int{1, 2, 3, 4, 5} {
		if _, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: n})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 5 && p[4] == 5
	}, 3*time.Second, 10*time.Millisecond,
		"non-contiguous offsets with no compaction must fold every record, not halt on the first gap")
	require.False(t, app.logs["k"].halted, "a benign non-contiguous gap must not halt the projector")
}

// A gap with NO snapshot at all cannot be a lost prefix: compaction always writes
// a snapshot FIRST (snapshot-FIRST invariant), so no snapshot ⇒ no compaction.
// The record must fold, not halt.
func TestGapWithNoSnapshotFoldsInsteadOfHalting(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "no snapshot ⇒ no compaction ⇒ the record must fold")
	require.False(t, ls.halted)
	require.False(t, spy.saw("via.events.compaction_gap_halt"))
}

// An UNCOMPACTED snapshot that cannot bridge a gap is a disposable cache, not
// evidence of a discarded prefix → benign fold, never halt.
func TestGapWithUncompactedUnbridgeableSnapshotFolds(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Forward of cur (5) but short of needCovered (8); UNCOMPACTED → just a cache.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 6, CodecHash: "h", V: mustJSON(6), Compacted: false})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "an uncompacted snapshot that can't bridge is a cache, not a lost prefix → fold")
	require.False(t, ls.halted)
}

// Safety preserved: a COMPACTED snapshot that cannot bridge a gap means a real
// prefix was discarded → HALT rather than silently truncate to the surviving tail.
func TestGapWithCompactedUnbridgeableSnapshotStillHalts(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 6, CodecHash: "h", V: mustJSON(6), Compacted: true})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.False(t, advanced)
	require.True(t, ls.halted, "a compacted prefix we can't bridge must halt, never truncate")
	require.True(t, spy.saw("via.events.compaction_gap_halt"))
}

// countingSnapshots counts LoadSnapshot calls, so a test can prove the projector
// does NOT re-read a snapshot on every non-contiguous record.
type countingSnapshots struct {
	Backplane
	loads *int32
}

func (c countingSnapshots) LoadSnapshot(ctx context.Context, key string) ([]byte, Rev, bool, error) {
	atomic.AddInt32(c.loads, 1)
	return c.Backplane.LoadSnapshot(ctx, key)
}

// An UNREADABLE snapshot (corrupt bytes) is treated as no usable snapshot — the
// same as cold start, which ignores a snapshot it cannot unmarshal. A gap behind
// it is benign (a disposable cache can't prove a lost prefix), so fold, not halt.
func TestGapWithUnreadableSnapshotFolds(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	if _, err := app.backplane.CAS(context.Background(), snapKey("k"), 0, []byte("not-json")); err != nil {
		t.Fatalf("seed corrupt snapshot: %v", err)
	}
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "an unreadable snapshot is not evidence of compaction → fold")
	require.False(t, ls.halted)
}

// A snapshot that bridges the gap and matches the codec but whose payload fails
// to DECODE is an unusable cache; if it is UNCOMPACTED no prefix was lost, so the
// gap is benign (fold), mirroring cold start's re-fold-from-genesis on a decode
// failure rather than halting.
func TestGapWithUndecodableUncompactedSnapshotFolds(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Bridges (CoveredOffset 8 >= needCovered 8, > cur 5), codec matches, but V is
	// a JSON string — decodeSnap (unmarshal into the int counter) fails; UNCOMPACTED → benign.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 8, CodecHash: "h", V: mustJSON("not-an-int"), Compacted: false})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "an undecodable uncompacted snapshot is a cache, not a lost prefix → fold")
	require.False(t, ls.halted)
}

// Once a benign gap is seen the projector must keep folding subsequent
// non-contiguous records WITHOUT re-reading a snapshot or re-halting — a busy
// shared stream produces a gap on essentially every record, so a per-record
// LoadSnapshot would be a real cost. The latch must read the snapshot at most
// once.
func TestProjectorKeepsFoldingAfterFirstBenignGapWithoutRereadingSnapshot(t *testing.T) {
	t.Parallel()
	var loads int32
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(countingSnapshots{Backplane: InMemory(), loads: &loads}))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	for _, off := range []Offset{9, 13, 21, 40} { // all non-contiguous
		require.True(t, app.applyRecord(ls, "k", Record{Key: "k", Offset: off, Data: goodBenchEnv()}),
			"record at offset %d must fold", off)
		require.False(t, ls.halted)
	}
	require.LessOrEqual(t, atomic.LoadInt32(&loads), int32(1),
		"a non-contiguous backend must not pay a LoadSnapshot per record — the benign-gap latch must hold")
}
