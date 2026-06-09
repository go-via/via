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

// The projector must actually persist snapshots, so a future cold start can use
// them. With an interval of 1, a snapshot appears in the Store after folds,
// covering the latest offset and decoding to the current projection.
func TestSnapshot_projectorPersistsSnapshots(t *testing.T) {
	t.Parallel()
	app := New(WithSnapshotInterval(1))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
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
func TestSnapshot_writesDisabledWhenIntervalNonPositive(t *testing.T) {
	t.Parallel()
	app := New(WithSnapshotInterval(0))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
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
func TestSnapshot_roundTripSeedsAFreshProjector(t *testing.T) {
	t.Parallel()
	app := New(WithSnapshotInterval(1))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
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
	appB := New(WithBackplane(app.backplane))
	serverB := httptest.NewServer(appB)
	t.Cleanup(serverB.Close)
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
func TestSnapshot_writeNeverRegressesCoveredOffset(t *testing.T) {
	t.Parallel()
	app := New()
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
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
