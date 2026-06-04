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

// A snapshot written by an incompatible V codec must be IGNORED and the key
// re-folded from genesis — so evolving the projection type V is free (the
// snapshot is a disposable cache, invalidated on a codec-hash mismatch).
func TestColdStartIgnoresSnapshotOnCodecHashMismatch(t *testing.T) {
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
