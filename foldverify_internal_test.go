package via

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// flakyEv is a deliberately IMPURE reducer: it appends a per-call counter, so
// folding the same (acc, ev) twice yields different results — exactly the bug
// fold-verify must catch (a real fold that read time/RNG/a mutable global).
var flakyFoldCounter int32

type flakyEv struct{ N int }

func (flakyEv) Fold(acc []int, e flakyEv) []int {
	return append(append([]int(nil), acc...), e.N, int(atomic.AddInt32(&flakyFoldCounter, 1)))
}

// envFor builds a current-version envelope for any event type, exactly as Append
// does on the wire (so the projector's decode path is exercised).
func envFor[E any](e E) []byte {
	d, _ := json.Marshal(e)
	b, err := json.Marshal(eventEnvelope{T: eventTypeTag[E](), V: currentVersionFor[E](), D: d})
	if err != nil {
		panic(err)
	}
	return b
}

func bindFlaky(app *App, key string) {
	var h StateAppEvents[flakyEv, []int]
	h.bindWireKey(key)
	h.bindApp(app)
}

// A snapshot crystallizes the projection into durable genesis once a key
// compacts; if the fold that produced it is non-deterministic, that corruption
// is frozen forever and re-folds can never recover. WithFoldVerify is the guard:
// it re-folds each record and, on a mismatch, emits via.fold.divergence AND
// REFUSES to compact the key — never letting a proven-non-deterministic fold
// reach durable genesis.
func TestFoldVerifyDetectsImpureFoldAndBlocksCompaction(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy),
		WithSnapshotInterval(1), WithFoldVerify())
	defer server.Close()
	defer app.backplane.Close()
	bindFlaky(app, "k")
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		if _, err := app.backplane.Append(ctx, "k", envFor(flakyEv{N: i})); err != nil {
			t.Fatalf("append: %v", err)
		}
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
func TestFoldVerifyAllowsPureFoldToCompact(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy),
		WithSnapshotInterval(1), WithFoldVerify())
	defer server.Close()
	defer app.backplane.Close()
	bindLog(app, "k") // envEv.Fold is pure
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		if _, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	require.Eventually(t, func() bool { return lowestRetainedOffset(t, app.backplane, "k") > 1 },
		2*time.Second, 10*time.Millisecond, "a pure fold must still compact under WithFoldVerify")
	require.False(t, spy.saw("via.fold.divergence"), "a pure fold must not be flagged as divergent")
}

// Fold-verify is OPT-IN: by default (no WithFoldVerify) the projector does NOT
// pay the double-fold cost, so an impure fold is NOT flagged and compaction is
// not blocked by it. This pins the cost as opt-in (the council's dev-mode
// framing) rather than always-on.
func TestFoldVerifyIsOptInAndOffByDefault(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy), WithSnapshotInterval(1))
	defer server.Close()
	defer app.backplane.Close()
	bindFlaky(app, "k")
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		if _, err := app.backplane.Append(ctx, "k", envFor(flakyEv{N: i})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// Without WithFoldVerify the divergence signal is never raised (no double-fold).
	require.Never(t, func() bool { return spy.saw("via.fold.divergence") },
		400*time.Millisecond, 50*time.Millisecond,
		"fold-verify must be opt-in: no divergence signal when disabled")
}
