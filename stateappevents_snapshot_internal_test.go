package via

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeContrivedSnapshot stores a checkpoint at snapKey(key) directly, so a
// cold-starting projector can be observed seeding from it.
func writeContrivedSnapshot(t *testing.T, app *App, key string, cp checkpoint) {
	t.Helper()
	b, err := json.Marshal(cp)
	require.NoError(t, err)
	_, err = app.backplane.CAS(context.Background(), snapKey(key), 0, b)
	require.NoError(t, err)
}

// Cold start must replay only the TAIL from a snapshot, not re-fold the whole
// log — that is the entire point of snapshots (an old key with millions of
// events resumes instantly). We prove it with a snapshot whose value could ONLY
// come from the snapshot, never from re-folding the real log.
func TestColdStartSeedsFromSnapshotAndReplaysOnlyTheTail(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	ctx := context.Background()

	// 5 real events in the log (offsets 1..5) — their fold would be [1..5].
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	// A snapshot covering offset 5 whose projection is the DISTINCT [99] —
	// reachable only by seeding from the snapshot, never by re-folding 1..5.
	writeContrivedSnapshot(t, app, "k", checkpoint{
		CoveredOffset: 5,
		CodecHash:     reflect.TypeFor[[]int]().String(),
		V:             json.RawMessage(`[99]`),
	})

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app) // cold-starts the projector

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 1 && p[0] == 99
	}, 2*time.Second, 10*time.Millisecond,
		"cold start must seed from the snapshot ([99]), not re-fold the log ([1..5])")
	require.Equal(t, Offset(5), logCursor(app, "k"), "cursor must resume at the snapshot's covered offset")

	// Only the tail (offset 6+) is folded onto the seeded projection.
	_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 6}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 2 && p[0] == 99 && p[1] == 6
	}, 2*time.Second, 10*time.Millisecond, "the tail folds onto the snapshot seed")
}

// A snapshot folded BEFORE a crypto-shred erasure (its ErasureGen below the
// authoritative generation) may hold now-shredded PII in its folded V, so cold
// start must IGNORE it and re-fold from the (now-undecryptable) log. Proven with
// the distinct-[99] trick: a stale-gen snapshot is ignored → projection re-folds
// the real log ([1..5]); were it (wrongly) seeded, the projection would be [99].
// This asserts the gen-invalidation directly, not transitively through erasure.
func TestColdStartIgnoresSnapshotBelowAuthoritativeErasureGen(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	// Advance the authoritative erasure generation to 2.
	for i := 0; i < 2; i++ {
		_, err := app.backplane.CAS(ctx, erasureGenKey, Rev(i), mustJSON(uint64(i+1)))
		require.NoError(t, err)
	}
	// A codec-MATCHING snapshot with the distinct value [99], but stamped at the
	// STALE generation 1 (< authoritative 2) — must be ignored.
	writeContrivedSnapshot(t, app, "k", checkpoint{
		CoveredOffset: 5,
		CodecHash:     reflect.TypeFor[[]int]().String(),
		V:             json.RawMessage(`[99]`),
		ErasureGen:    1,
	})

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 5 && p[0] == 1 && p[4] == 5
	}, 2*time.Second, 10*time.Millisecond,
		"a stale-gen snapshot must be ignored and the log re-folded ([1..5]), not seeded ([99])")
}

// The mirror of the stale-gen case: a snapshot stamped at OR ABOVE the
// authoritative erasure generation is still a valid cache and IS seeded — gen
// invalidation must not nuke every snapshot, only pre-erasure ones.
func TestColdStartSeedsSnapshotAtOrAboveAuthoritativeErasureGen(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	_, err := app.backplane.CAS(ctx, erasureGenKey, 0, mustJSON(uint64(1)))
	require.NoError(t, err)
	writeContrivedSnapshot(t, app, "k", checkpoint{
		CoveredOffset: 5,
		CodecHash:     reflect.TypeFor[[]int]().String(),
		V:             json.RawMessage(`[99]`),
		ErasureGen:    1, // == authoritative → still valid
	})

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 1 && p[0] == 99
	}, 2*time.Second, 10*time.Millisecond,
		"a current-gen snapshot must still be seeded ([99]), not needlessly re-folded")
}

// A snapshot written by an incompatible V codec must be IGNORED and the key
// re-folded from genesis — so evolving the projection type V is free (the
// snapshot is a disposable cache, invalidated on a codec-hash mismatch).
func TestColdStartIgnoresSnapshotOnCodecHashMismatch(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	writeContrivedSnapshot(t, app, "k", checkpoint{
		CoveredOffset: 5,
		CodecHash:     "mismatch", // wrong → invalidates the cache
		V:             json.RawMessage(`[99]`),
	})

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 5 && p[0] == 1 && p[4] == 5
	}, 2*time.Second, 10*time.Millisecond,
		"a codec-hash mismatch must ignore the snapshot and re-fold from genesis ([1..5], not [99])")
}

// The projector must actually persist snapshots, so a future cold start can use
// them. With an interval of 1, a snapshot appears in the Store after folds,
// covering the latest offset and decoding to the current projection.
func TestProjectorPersistsSnapshots(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)

	for i := 1; i <= 3; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		data, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
		if !ok {
			return false
		}
		var cp checkpoint
		if json.Unmarshal(data, &cp) != nil {
			return false
		}
		var v []int
		_ = json.Unmarshal(cp.V, &v)
		return cp.CoveredOffset == 3 && len(v) == 3 && v[2] == 3 &&
			cp.CodecHash == reflect.TypeFor[[]int]().String()
	}, 2*time.Second, 10*time.Millisecond,
		"the projector must persist a snapshot covering the latest offset with the folded value")
}

// WithSnapshotInterval(0) disables snapshot writes — an operator opt-out the
// public Option promises. After many folds the Store must hold no snapshot, so
// the feature is genuinely off (not merely deferred to a larger threshold).
func TestSnapshotWritesDisabledWhenIntervalNonPositive(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(0))
	defer server.Close()
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)

	for i := 1; i <= 20; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	// Wait until all 20 folds have landed, then assert NO snapshot was written.
	require.Eventually(t, func() bool { return logCursor(app, "k") == 20 },
		2*time.Second, 10*time.Millisecond, "all events must fold")
	_, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
	require.False(t, ok, "interval<=0 must write no snapshot")
}

// The real end-to-end the feature exists for: a projector PERSISTS a snapshot,
// then a FRESH projector on the SAME backplane (a restart / new pod) cold-starts
// from that very snapshot and folds only the tail — proving write and cold-start
// interoperate, not just each half against a contrived fixture.
func TestRoundTripPersistedSnapshotSeedsAFreshProjector(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	// Pod A folds 3 events and persists a snapshot covering offset 3.
	var hA StateAppEvents[envEv, []int]
	hA.bindWireKey("k")
	hA.bindApp(app)
	for i := 1; i <= 3; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		_, _, ok, _ := app.backplane.LoadSnapshot(ctx, snapKey("k"))
		return ok
	}, 2*time.Second, 10*time.Millisecond, "pod A must persist a snapshot")

	// Pod B: a fresh App sharing the SAME backplane, a brand-new projector.
	appB := New(WithTestServer(&server), WithBackplane(app.backplane))
	var hB StateAppEvents[envEv, []int]
	hB.bindWireKey("k")
	hB.bindApp(appB) // cold-starts from pod A's persisted snapshot

	require.Eventually(t, func() bool {
		p := projection(appB, "k")
		return len(p) == 3 && p[0] == 1 && p[2] == 3
	}, 2*time.Second, 10*time.Millisecond,
		"pod B must seed from the persisted snapshot, reaching the folded projection")
	require.Equal(t, Offset(3), logCursor(appB, "k"), "pod B resumes at the persisted covered offset")

	// A tail event reaches pod B and folds onto the seeded projection.
	_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 4}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p := projection(appB, "k")
		return len(p) == 4 && p[3] == 4
	}, 2*time.Second, 10*time.Millisecond, "pod B folds the tail onto the seeded snapshot")
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
