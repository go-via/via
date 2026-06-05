package via

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// manualLogState wires a logState for benchEv/[]int-style int-counter folds
// WITHOUT starting the live projector goroutine, so applyRecord can be driven
// deterministically (no race with a background tailer).
func manualLogState(app *App, key string, cursor Offset, codecHash string) *logState {
	ls := &logState{
		projection: int(cursor), // counter fold: projection == events folded so far
		seed:       0,
		cursor:     cursor,
		epochSeen:  true,
		codecHash:  codecHash,
		foldBytes: func(acc any, data []byte) (any, error) {
			ev, err := decodeEvent[benchEv](data, nil)
			if err != nil {
				return acc, err
			}
			cur, _ := acc.(int)
			return ev.Fold(cur, ev), nil
		},
		decodeSnap: func(b []byte) (any, error) {
			var v int
			if err := json.Unmarshal(b, &v); err != nil {
				return nil, err
			}
			return v, nil
		},
		encodeSnap: func(p any) ([]byte, error) { v, _ := p.(int); return json.Marshal(v) },
	}
	app.logsMu.Lock()
	app.logs[key] = ls
	app.logsMu.Unlock()
	return ls
}

func writeSnap(t *testing.T, app *App, key string, cp checkpoint) {
	t.Helper()
	b, _ := json.Marshal(cp)
	_, err := app.backplane.CAS(context.Background(), snapKey(key), 0, b)
	require.NoError(t, err)
}

// When compaction overtakes a live projector — it has folded only up to cursor C
// but the log has dropped records and the next delivered record has an offset
// well beyond C+1 (a GAP) — the projector must RE-SEED from the durable snapshot
// (which covers the gap) before folding, NOT fold the post-gap record onto its
// stale projection. Folding onto the stale value silently and permanently
// diverges this pod from its peers (the cross-pod truncation bug the multi-node
// load test surfaced).
func TestProjectorReseedsFromSnapshotOnCompactionGap(t *testing.T) {
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
func TestProjectorHaltsOnCompactionGapWithoutRecoverableSnapshot(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Snapshot exists but under a DIFFERENT codec hash → cannot bridge live.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 8, CodecHash: "OTHER", V: mustJSON(8)})

	advanced := app.applyRecord(ls, "k", Record{Key: "k", Offset: 9, Data: goodBenchEnv()})

	require.False(t, advanced)
	require.Equal(t, 5, ls.projection, "must NOT fold onto the stale projection")
	require.True(t, ls.halted, "an unrecoverable compaction gap halts the projector")
	require.True(t, spy.saw("via.events.compaction_gap_halt"))
}

// A contiguous record (offset == cursor+1) is the normal case: fold directly, no
// reseed, no snapshot read.
func TestProjectorFoldsContiguousRecordWithoutReseed(t *testing.T) {
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

func goodBenchEnv() []byte { return envFor(benchEv{N: 1}) }

// Boundary: the snapshot already covers the INCOMING record (CoveredOffset ==
// rec.Offset). The reseed must still fire (it bridges the gap and moves forward),
// but the post-reseed projectRecord must DEDUP the record (rec.Offset <= cursor)
// rather than double-fold it — the projection lands exactly at the snapshot value.
func TestProjectorReseedDedupsWhenSnapshotCoversIncomingRecord(t *testing.T) {
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
func TestProjectorHaltsWhenSnapshotDoesNotBridgeWholeGap(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	ls := manualLogState(app, "k", 5, "h")
	// Gap is 6..19 (rec at 20). Snapshot only covers 12 — forward of cur (5) but
	// short of needCovered (19): folding 20 onto 12 would skip 13..19.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 12, CodecHash: "h", V: mustJSON(12)})

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
func TestFreshProjectorReseedsWhenFirstRecordIsPostCompaction(t *testing.T) {
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

// The shared snapshot cell must be MONOTONIC in CoveredOffset: a lagging pod must
// not overwrite a leader's higher-covered snapshot with a lower one. If it could,
// compaction (which trusts the durable snapshot to cover the prefix it drops) and
// a peer's gap-reseed would recover a snapshot that doesn't bridge the gap.
func TestSnapshotWriteNeverRegressesCoveredOffset(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	ctx := context.Background()

	// A leader's snapshot already covers offset 100.
	writeSnap(t, app, "k", checkpoint{Epoch: 0, CoveredOffset: 100, CodecHash: "h", V: mustJSON(100)})

	// A lagging pod (cursor 40) attempts to write — must NOT regress the cell.
	ls := manualLogState(app, "k", 40, "h")
	app.writeSnapshot(ls, "k")

	data, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
	require.True(t, ok)
	var cp checkpoint
	require.NoError(t, json.Unmarshal(data, &cp))
	require.Equal(t, Offset(100), cp.CoveredOffset, "a lagging pod must not regress the shared snapshot's CoveredOffset")

	// A pod that genuinely advances it (cursor 150) DOES write.
	ls2 := manualLogState(app, "k", 150, "h")
	app.writeSnapshot(ls2, "k")
	data, _, _, _ = app.backplane.LoadSnapshot(ctx, snapKey("k"))
	require.NoError(t, json.Unmarshal(data, &cp))
	require.Equal(t, Offset(150), cp.CoveredOffset, "a higher-covered snapshot DOES advance the cell")
}
