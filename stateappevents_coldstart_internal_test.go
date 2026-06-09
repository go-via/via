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

// The payoff: a fresh pod sharing a backplane whose prefix has been compacted
// still cold-starts to the full projection — it seeds from the snapshot and
// never needs the discarded events.
func TestFreshProjectorColdStartsAfterPrefixCompacted(t *testing.T) {
	t.Parallel()
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
