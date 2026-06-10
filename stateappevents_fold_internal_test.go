package via

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// When a key's underlying stream is recreated / trimmed-to-empty / restored, its
// offset space restarts at 1 under a NEW epoch. A bare offset high-water-mark
// would then skip every new record (their offsets are <= the old cursor),
// silently freezing the projection. The projector must DETECT the epoch change
// and re-snapshot from genesis so the projection re-converges — emitting
// via.events.epoch_reset.
func TestFold_reSnapshotsOnEpochReset(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	app := New(WithMetrics(spy))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	ls := app.logs["k"]

	// Two records in the original epoch (0) fold normally.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 1, Data: goodEnv(t, envEv{N: 1})})
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 2, Data: goodEnv(t, envEv{N: 2})})
	require.Equal(t, []int{1, 2}, projection(app, "k"), "same-epoch records fold normally")
	require.False(t, spy.saw("via.events.epoch_reset"), "the baseline epoch must not be mistaken for a reset")

	// The stream resets: a NEW epoch (1) whose offsets restart at 1. This MUST
	// re-snapshot from genesis — the projection becomes just the new-epoch event,
	// NOT [1,2,9] (which a bare HWM that appended would produce) and NOT frozen at
	// [1,2] (which a bare HWM that skipped offset 1 <= cursor 2 would produce).
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 1, Offset: 1, Data: goodEnv(t, envEv{N: 9})})
	require.Equal(t, []int{9}, projection(app, "k"), "an epoch reset must re-snapshot from genesis")
	require.Equal(t, Offset(1), logCursor(app, "k"), "cursor restarts in the new epoch")
	require.True(t, spy.saw("via.events.epoch_reset"), "an offset-space reset must emit via.events.epoch_reset")

	// Subsequent new-epoch records fold onto the re-snapshotted projection.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 1, Offset: 2, Data: goodEnv(t, envEv{N: 10})})
	require.Equal(t, []int{9, 10}, projection(app, "k"), "new-epoch records fold after the reset")

	// A reset is detected by ANY epoch change, not only a forward bump — a
	// restore can roll the epoch backward and still restart the offset space.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 1, Data: goodEnv(t, envEv{N: 5})})
	require.Equal(t, []int{5}, projection(app, "k"), "a backward epoch change also re-snapshots from genesis")
}

// FuzzFold_isDeterministicAndNeverPanics drives arbitrary bytes through the real
// projector decode+fold path. The decode must classify every input (fold,
// ErrUndecodable, or ErrForwardIncompatible) WITHOUT panicking — a poison record
// must never crash a pod — and folding the same bytes from the same accumulator
// twice must yield the identical result and error (purity). A reducer that read
// a clock or RNG would fail the second-fold equality.
func FuzzFold_isDeterministicAndNeverPanics(f *testing.F) {
	app := New()
	server := httptest.NewServer(app)
	defer server.Close()
	fold := bindLog(app, "k")

	// Seed with well-formed envelopes and assorted poison.
	for _, n := range []int{0, 1, 7, -3, 1000} {
		f.Add(goodEnvBytes(n))
	}
	f.Add([]byte("garbage"))
	f.Add([]byte(`{"t":"envEv","v":99,"d":{"N":1}}`)) // forward-incompatible
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		acc := []int{1, 2, 3}
		r1, e1 := fold(append([]int(nil), acc...), data)
		r2, e2 := fold(append([]int(nil), acc...), data)

		// Same input → same error classification.
		require.Equal(t, e1 == nil, e2 == nil, "fold error determinism for %q", data)
		if e1 != nil {
			require.Equal(t, e1.Error(), e2.Error(), "fold error must be deterministic for %q", data)
			return
		}
		// Same input + same acc → identical projection (purity).
		got1, _ := r1.([]int)
		got2, _ := r2.([]int)
		require.Equal(t, got1, got2, "fold must be deterministic for %q", data)
	})
}

// TestFold_convergesAcrossProcesses replays a fixed event log in two SEPARATE OS
// processes and asserts both reach the identical projection digest. Same-process
// determinism (the fuzz above) cannot catch a reducer that reads process-global
// state (a package var, an env-seeded value) — two goroutines share it, two
// processes do not. This is the only gate that distinguishes "pure" from "agrees
// with itself in one process".
func TestFold_convergesAcrossProcesses(t *testing.T) {
	t.Parallel()
	if os.Getenv("VIA_FOLD_REPLAY_CHILD") == "1" {
		fmt.Printf("VIA_FOLD_DIGEST=%d\n", replayFixedLogDigest())
		return
	}
	d1 := runReplayChild(t)
	d2 := runReplayChild(t)
	require.NotZero(t, d1, "child must produce a digest")
	require.Equal(t, d1, d2, "two independent processes must fold the same log to the same digest")
}

// A snapshot crystallizes the projection into durable genesis once a key
// compacts; if the fold that produced it is non-deterministic, that corruption
// is frozen forever and re-folds can never recover. WithFoldVerify is the guard:
// it re-folds each record and, on a mismatch, emits via.fold.divergence AND
// REFUSES to compact the key — never letting a proven-non-deterministic fold
// reach durable genesis.
func TestFoldVerify_detectsImpureFoldAndBlocksCompaction(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	app := New(WithMetrics(spy),
		WithSnapshotInterval(1), WithFoldVerify())
	server := httptest.NewServer(app)
	defer server.Close()
	defer app.backplane.Close()
	bindFlaky(app, "k")
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		_, err := app.backplane.Append(ctx, "k", envFor(flakyEv{N: i}))
		require.NoError(t, err, "append")
	}

	// The impurity is detected.
	require.Eventually(t, func() bool { return spy.saw("via.fold.divergence") },
		2*time.Second, 10*time.Millisecond, "WithFoldVerify must detect a non-deterministic fold")

	// And the key must NEVER compact — the prefix stays at genesis (offset 1),
	// so the non-deterministic projection is never crystallized into durable
	// genesis.
	require.Never(t, func() bool { return lowestRetainedOffset(t, app.backplane, "k") > 1 },
		500*time.Millisecond, 50*time.Millisecond,
		"a fold proven non-deterministic must never be compacted")
}

// A PURE fold under WithFoldVerify must behave exactly as without it: no
// divergence signal, and compaction proceeds normally. The guard must not flag
// or block correct reducers.
func TestFoldVerify_allowsPureFoldToCompact(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	app := New(WithMetrics(spy),
		WithSnapshotInterval(1), WithFoldVerify())
	server := httptest.NewServer(app)
	defer server.Close()
	defer app.backplane.Close()
	bindLog(app, "k") // envEv.Fold is pure
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err, "append")
	}

	require.Eventually(t, func() bool { return lowestRetainedOffset(t, app.backplane, "k") > 1 },
		2*time.Second, 10*time.Millisecond, "a pure fold must still compact under WithFoldVerify")
	require.False(t, spy.saw("via.fold.divergence"), "a pure fold must not be flagged as divergent")
}

// Fold-verify is OPT-IN: by default (no WithFoldVerify) the projector does NOT
// pay the double-fold cost, so an impure fold is NOT flagged and compaction is
// not blocked by it. This keeps the cost opt-in (a dev-mode concern)
// rather than always-on.
func TestFoldVerify_isOptInAndOffByDefault(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	app := New(WithMetrics(spy), WithSnapshotInterval(1))
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	defer server.Close()
	defer app.backplane.Close()
	bindFlaky(app, "k")
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		_, err := app.backplane.Append(ctx, "k", envFor(flakyEv{N: i}))
		require.NoError(t, err, "append")
	}
	// Without WithFoldVerify the divergence signal is never raised (no double-fold).
	require.Never(t, func() bool { return spy.saw("via.fold.divergence") },
		400*time.Millisecond, 50*time.Millisecond,
		"fold-verify must be opt-in: no divergence signal when disabled")
}
