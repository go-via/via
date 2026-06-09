package via

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A backend whose per-key offsets are non-contiguous (a shared JetStream stream
// sequenced across subjects) must NOT be mistaken for a compacted log: with no
// compaction — hence no snapshot — every event must still fold. This is the
// viashowcase multi-log bug, where the projector halted on the very first event
// (offset > 1) and silently dropped them all.
func TestGap_foldsNonContiguousOffsetsWhenNothingWasCompacted(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(gappedOffsets{Backplane: InMemory(), stride: 3}))
	defer server.Close()
	bindLog(app, "k")
	ctx := context.Background()

	for _, n := range []int{1, 2, 3, 4, 5} {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: n}))
		require.NoError(t, err, "append")
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
func TestGap_withNoSnapshotFoldsInsteadOfHalting(t *testing.T) {
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
func TestGap_withUncompactedUnbridgeableSnapshotFolds(t *testing.T) {
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
func TestGap_withCompactedUnbridgeableSnapshotStillHalts(t *testing.T) {
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

// An UNREADABLE snapshot (corrupt bytes) is treated as no usable snapshot — the
// same as cold start, which ignores a snapshot it cannot unmarshal. A gap behind
// it is benign (a disposable cache can't prove a lost prefix), so fold, not halt.
func TestGap_withUnreadableSnapshotFolds(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	_, err := app.backplane.CAS(context.Background(), snapKey("k"), 0, []byte("not-json"))
	require.NoError(t, err, "seed corrupt snapshot")
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "an unreadable snapshot is not evidence of compaction → fold")
	require.False(t, ls.halted)
}

// A snapshot that bridges the gap and matches the codec but whose payload fails
// to DECODE is an unusable cache; if it is UNCOMPACTED no prefix was lost, so the
// gap is benign (fold), mirroring cold start's re-fold-from-genesis on a decode
// failure rather than halting.
func TestGap_withUndecodableUncompactedSnapshotFolds(t *testing.T) {
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
func TestGap_keepsFoldingAfterFirstBenignGapWithoutRereadingSnapshot(t *testing.T) {
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

// When compaction overtakes a live projector — it has folded only up to cursor C
// but the log has dropped records and the next delivered record has an offset
// well beyond C+1 (a GAP) — the projector must RE-SEED from the durable snapshot
// (which covers the gap) before folding, NOT fold the post-gap record onto its
// stale projection. Folding onto the stale value silently and permanently
// diverges this pod from its peers (the cross-pod truncation bug the multi-node
// load test surfaced).
func TestGap_reseedsFromSnapshotOnCompactionGap(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	// Projector has folded up to offset 5 (projection == 5).
	ls := manualLogState(app, "k", 5, "h")
	// A durable snapshot covers offset 8 with the correct folded value (8).
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 8, CodecHash: "h", V: mustJSON(8)})

	// The next delivered record is offset 9 — a GAP (offsets 6,7,8 were dropped
	// by compaction and never folded by this projector).
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.True(t, advanced, "folding the post-reseed record advances")
	require.Equal(t, 9, ls.projection, "must re-seed to the snapshot (8) then fold offset 9 → 9, NOT stale-fold 5→6")
	require.Equal(t, Offset(9), ls.cursor)
	require.True(t, spy.saw("via.events.compaction_reseed"), "a compaction-gap reseed must be metered")
}

// If compaction overtakes the projector but NO snapshot can recover the gap
// (missing, or a codec mismatch that can't be bridged live), the projector must
// HALT (roll-forward-only) rather than fold onto a stale projection — fail safe,
// never silently diverge.
func TestGap_haltsOnCompactionGapWithoutRecoverableSnapshot(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Snapshot is COMPACTED (a prefix was discarded) but under a DIFFERENT codec
	// hash → cannot bridge live; a genuine lost prefix must halt, not truncate.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 8, CodecHash: "OTHER", V: mustJSON(8), Compacted: true})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.False(t, advanced)
	require.Equal(t, 5, ls.projection, "must NOT fold onto the stale projection")
	require.True(t, ls.halted, "an unrecoverable compaction gap halts the projector")
	require.True(t, spy.saw("via.events.compaction_gap_halt"))
}

// A contiguous record (offset == cursor+1) is the normal case: fold directly, no
// reseed, no snapshot read.
func TestGap_foldsContiguousRecordWithoutReseed(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 6, Data: goodBenchEnv()})

	require.True(t, advanced)
	require.Equal(t, 6, ls.projection, "contiguous fold: 5 → 6")
	require.False(t, spy.saw("via.events.compaction_reseed"), "no reseed on a contiguous record")
}

// Boundary: the snapshot already covers the INCOMING record (CoveredOffset ==
// rec.Offset). The reseed must still fire (it bridges the gap and moves forward),
// but the post-reseed projectRecord must DEDUP the record (rec.Offset <= cursor)
// rather than double-fold it — the projection lands exactly at the snapshot value.
func TestGap_reseedDedupsWhenSnapshotCoversIncomingRecord(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Snapshot covers offset 9 — the very record about to be delivered.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 9, CodecHash: "h", V: mustJSON(9)})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.False(t, advanced, "the incoming record is already in the snapshot → no advance, no broadcast")
	require.Equal(t, 9, ls.projection, "projection lands at the snapshot value, NOT folded to 10")
	require.Equal(t, Offset(9), ls.cursor)
	require.True(t, spy.saw("via.events.compaction_reseed"), "the reseed itself still fires")
}

// Boundary: a snapshot that moves forward (CoveredOffset > cur) but does NOT
// bridge the whole gap (CoveredOffset < rec.Offset-1) must HALT, not partial-seed
// and fold — folding rec onto a projection short of the gap still diverges.
func TestGap_haltsWhenSnapshotDoesNotBridgeWholeGap(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Gap is 6..19 (rec at 20). COMPACTED snapshot only covers 12 — forward of cur
	// (5) but short of needCovered (19): folding 20 onto 12 would skip 13..19, and
	// the discarded prefix means we cannot recover them → halt.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 12, CodecHash: "h", V: mustJSON(12), Compacted: true})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 20, Data: goodBenchEnv()})

	require.False(t, advanced)
	require.Equal(t, 5, ls.projection, "must NOT partial-seed to 12 and fold — that skips 13..19")
	require.True(t, ls.halted, "a snapshot that does not bridge the WHOLE gap halts")
	require.True(t, spy.saw("via.events.compaction_gap_halt"))
}

// A FRESH projector (cursor 0, nothing folded yet) whose FIRST delivered record
// has an offset > 1 means the log's prefix was compacted before its subscriber
// first read — it must re-seed from the snapshot, NOT fold offset N onto the
// seed (which would set proj=1,cursor=N and diverge by N-1 forever). This is the
// exact multi-node truncation the load test caught: a slow pod whose first read
// races a peer's compaction.
func TestGap_freshProjectorReseedsWhenFirstRecordIsPostCompaction(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 0, "h") // fresh: cursor 0, projection 0, never folded
	ls.epochSeen = false
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 255, CodecHash: "h", V: mustJSON(255)})

	// First delivered record is offset 256 (offsets 1..255 were compacted away).
	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 256, Data: goodBenchEnv()})

	require.True(t, advanced)
	require.Equal(t, 256, ls.projection, "must re-seed to 255 then fold offset 256 → 256, NOT fold onto seed → 1")
	require.Equal(t, Offset(256), ls.cursor)
	require.True(t, spy.saw("via.events.compaction_reseed"))
}
