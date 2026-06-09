package via

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// compactedKeyWithMismatchedSnapshot sets up the durable-genesis scenario: a key
// whose log prefix has been physically compacted away (offsets 1..3 gone, 4..5
// retained) and whose stored checkpoint carries Compacted:true, a covered offset
// of 5, and an OLD codec hash (≠ the current V codec). A naive re-fold from 0
// would yield only [4,5] (the retained tail) — silent truncation — so the
// distinct snapshot value proves which path ran.
func compactedKeyWithMismatchedSnapshot(t *testing.T, app *App, key, oldHash string, oldV []int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, key, goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	require.NoError(t, app.backplane.(Compactor).Compact(ctx, key, 4)) // drop offsets 1..3
	vb, err := json.Marshal(oldV)
	require.NoError(t, err)
	writeContrivedSnapshot(t, app, key, checkpoint{
		Compacted:     true,
		CoveredOffset: 5,
		CodecHash:     oldHash, // ≠ the current reflect.TypeFor[[]int]() hash
		V:             vb,
	})
}

// The load-bearing link between P5b compaction and P5c migration detection: the
// REAL projector must stamp Compacted:true on the durable checkpoint once it has
// discarded a prefix. If it never did, a genuinely compacted key would be
// mis-classified as a disposable cache and silently truncate on a codec change.
//
//nolint:paralleltest // mutates the process-global snapshot-migration registry
func TestMigrate_marksCheckpointCompactedAfterDiscardingPrefix(t *testing.T) {
	app := New(WithSnapshotInterval(1))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()
	ctx := context.Background()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	// ≥3 events at interval 1 ⇒ prevSnapOffset climbs to ≥2 ⇒ a prefix is dropped
	// ⇒ the checkpoint written from then on must read Compacted:true.
	for i := 1; i <= 5; i++ {
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
		return cp.Compacted
	}, 2*time.Second, 10*time.Millisecond,
		"the durable checkpoint must read Compacted:true once the projector has discarded a prefix")
}

// A COMPACTED key whose snapshot codec changed must run the registered seeded
// migration (decode old V → seed, fold the retained tail on top), NEVER discard
// and re-fold — the deleted prefix is unrecoverable, so discarding would silently
// truncate the value to whatever events happen to survive.
//
//nolint:paralleltest // mutates the process-global snapshot-migration registry
func TestMigrate_runsSeededMigrationOnCodecMismatch(t *testing.T) {
	const oldHash = "p5c.test.seeded.oldhash"
	RegisterSnapshotMigration(oldHash, func(b []byte) ([]int, error) {
		var v []int
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return v, nil // identity migration: old []int → current []int
	})
	defer deleteSnapMigration(oldHash)

	app := New()
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()
	compactedKeyWithMismatchedSnapshot(t, app, "k", oldHash, []int{10, 20, 30, 40, 50})

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app) // cold-starts; mismatch + compacted → seeded migration

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 5 && p[0] == 10 && p[4] == 50
	}, 2*time.Second, 10*time.Millisecond,
		"must seed from the migrated snapshot ([10..50]), never re-fold the truncated tail ([4,5])")

	// The retained tail folds onto the migrated seed.
	_, err := app.backplane.Append(context.Background(), "k", goodEnv(t, envEv{N: 6}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 6 && p[5] == 6
	}, 2*time.Second, 10*time.Millisecond, "the tail folds onto the migrated seed")
}

// A COMPACTED key with NO registered migration for its old codec must HALT —
// roll-forward-only — and emit via.snapshot.unbridgeable. A stuck projector is
// the safe failure; silently truncating to the surviving tail is not.
//
//nolint:paralleltest // mutates the process-global snapshot-migration registry
func TestMigrate_haltsWhenNoMigrationRegistered(t *testing.T) {
	const oldHash = "p5c.test.unbridgeable.oldhash" // never registered
	spy := &spyMetrics{}
	app := New(WithMetrics(spy))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()
	compactedKeyWithMismatchedSnapshot(t, app, "k", oldHash, []int{10, 20, 30, 40, 50})

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)

	require.Eventually(t, func() bool { return spy.saw("via.snapshot.unbridgeable") },
		2*time.Second, 10*time.Millisecond, "an unbridgeable compacted snapshot must emit the metric")

	// Halted: the projection never truncates to the surviving tail [4,5] and a
	// later append is not folded.
	require.Empty(t, projection(app, "k"), "a halted projector must not truncate to the retained tail")
	_, err := app.backplane.Append(context.Background(), "k", goodEnv(t, envEv{N: 6}))
	require.NoError(t, err)
	require.Never(t, func() bool { return len(projection(app, "k")) > 0 },
		500*time.Millisecond, 25*time.Millisecond, "a halted projector folds nothing further")
}

// A migration that ERRORS is no safer than none: the key must HALT (never
// truncate), so a broken migration fails closed rather than producing a
// plausible-but-wrong value.
//
//nolint:paralleltest // mutates the process-global snapshot-migration registry
func TestMigrate_haltsWhenMigrationErrors(t *testing.T) {
	const oldHash = "p5c.test.migration-error.oldhash"
	RegisterSnapshotMigration(oldHash, func([]byte) ([]int, error) {
		return nil, context.DeadlineExceeded // any error
	})
	defer deleteSnapMigration(oldHash)
	spy := &spyMetrics{}
	app := New(WithMetrics(spy))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()
	compactedKeyWithMismatchedSnapshot(t, app, "k", oldHash, []int{10, 20, 30})

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)

	require.Eventually(t, func() bool { return spy.saw("via.snapshot.unbridgeable") },
		2*time.Second, 10*time.Millisecond, "a failed migration must halt and emit the metric")
	require.Empty(t, projection(app, "k"), "a failed migration must not truncate to the retained tail")
}

// An UNCOMPACTED key still discards + re-folds from genesis on a codec-hash
// mismatch — V evolution stays free for the common case; the durable-genesis
// path must NOT change this when the prefix is intact.
//
//nolint:paralleltest // mutates the process-global snapshot-migration registry
func TestMigrate_uncompactedKeyRefoldsFromGenesisOnMismatch(t *testing.T) {
	app := New()
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	// Compacted:false + mismatched hash, prefix INTACT (no Compact call).
	writeContrivedSnapshot(t, app, "k", checkpoint{
		Compacted:     false,
		CoveredOffset: 5,
		CodecHash:     "p5c.test.uncompacted.oldhash",
		V:             json.RawMessage(`[99]`),
	})

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)

	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 5 && p[0] == 1 && p[4] == 5
	}, 2*time.Second, 10*time.Millisecond,
		"an uncompacted mismatch must re-fold from genesis ([1..5]), not seed from the stale snapshot ([99])")
}
