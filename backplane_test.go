package via_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The keystone of the whole backplane design: two independent App instances
// (two "pods") wired to ONE shared backplane must CONVERGE. An event appended
// through pod A's StateAppEvents has to surface, folded, in pod B's projection —
// because B's per-key projector tails the shared log, NOT because A folded
// locally. A local-fold-in-Append implementation would pass every single-pod
// test yet FAIL here: A's projection would update but B would never see it.
//
// Two Apps in one process sharing one in-memory backplane is the infra-free
// stand-in for two pods sharing one NATS — no network, no external server.
func TestTwoAppsShareOneBackplaneAndConverge(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	var serverA, serverB *httptest.Server
	appA := via.New(via.WithTestServer(&serverA), via.WithBackplane(shared))
	via.Mount[feedPage](appA, "/")
	defer serverA.Close()

	appB := via.New(via.WithTestServer(&serverB), via.WithBackplane(shared))
	via.Mount[feedPage](appB, "/")
	defer serverB.Close()

	// A client on each pod; B watches live so we observe the cross-pod re-render.
	a := vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	// Append on pod A.
	require.Equal(t, 200, a.Action("Add").Fire())

	// Pod B's projector tails the shared log, folds the event, and re-renders
	// B's live tab — convergence without A ever touching B.
	vt.AwaitFrame(t, framesB, 2*time.Second, `<div id="feed">hello</div>`)
}

// Convergence is symmetric and durable: after appends on BOTH pods, a brand-new
// reader on EITHER pod sees the full, identically-ordered folded log — the
// shared backplane is the single source of order, so neither pod's projection
// diverges.
func TestCrossPodProjectionsAgreeForFreshReaders(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	var serverA, serverB *httptest.Server
	appA := via.New(via.WithTestServer(&serverA), via.WithBackplane(shared))
	via.Mount[feedPage](appA, "/")
	defer serverA.Close()

	appB := via.New(via.WithTestServer(&serverB), via.WithBackplane(shared))
	via.Mount[feedPage](appB, "/")
	defer serverB.Close()

	// Mount a client on each pod so both projectors are running, then append
	// from each side.
	a := vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	require.Equal(t, 200, a.Action("Add").Fire())
	require.Equal(t, 200, b.Action("Add").Fire())

	// A fresh reader on each pod must converge to the same two-item feed.
	const want = `<div id="feed">hello,hello</div>`
	require.Eventually(t, func() bool {
		return strings.Contains(vt.NewClient(t, serverA, "/").HTML(), want)
	}, 2*time.Second, 20*time.Millisecond, "pod A must converge to both events")
	require.Eventually(t, func() bool {
		return strings.Contains(vt.NewClient(t, serverB, "/").HTML(), want)
	}, 2*time.Second, 20*time.Millisecond, "pod B must converge to the same feed")
}

// The value-shaped counterpart of the StateAppEvents cross-pod keystone: a
// value-shaped StateApp.Update on pod A must converge on pod B when both share
// one backplane. Today StateApp is pod-local (its own appStore), so B never sees
// A's write — this proves the Store-as-source-of-truth value path. (appCounterPage
// is defined in stateapp_test.go: a StateAppNum[int] "visits" + Bump action +
// a <span id="visits"> view.)
func TestStateAppConvergesAcrossPods(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	var serverA, serverB *httptest.Server
	appA := via.New(via.WithTestServer(&serverA), via.WithBackplane(shared))
	via.Mount[appCounterPage](appA, "/")
	defer serverA.Close()

	appB := via.New(via.WithTestServer(&serverB), via.WithBackplane(shared))
	via.Mount[appCounterPage](appB, "/")
	defer serverB.Close()

	a := vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	require.Equal(t, 200, a.Action("Bump").Fire())

	// Pod B's changes-tailer re-pulls the Store cell A wrote and re-renders B's
	// live tab — value-shaped convergence with no shared process state.
	vt.AwaitFrame(t, framesB, 2*time.Second, `<span id="visits">1</span>`)

	// A fresh reader on pod B also sees the converged value.
	require.Eventually(t, func() bool {
		return strings.Contains(vt.NewClient(t, serverB, "/").HTML(), `<span id="visits">1</span>`)
	}, 2*time.Second, 20*time.Millisecond, "a fresh reader on pod B must see pod A's write")
}

// The periodic reconcile sweep is what makes the changes feed a pure latency
// optimization: a pod must converge to the Store HEAD even when no Change hint
// ever reaches it. A SILENT (sync-off) Update writes the Store but suppresses
// the hint, so pod B's changes-tailer never fires — only the sweep can carry
// B to the value A wrote. (syncOffAppPage lives in ctx_test.go: StateAppNum[int]
// Visits, BumpSilently = SyncOff + Visits.Update(+1), <span id="visits"> view.)
func TestReconcileSweepConvergesPeerWithoutAChangeHint(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()
	interval := via.WithReconcileInterval(50 * time.Millisecond)

	var serverA, serverB *httptest.Server
	appA := via.New(via.WithTestServer(&serverA), via.WithBackplane(shared), interval)
	via.Mount[syncOffAppPage](appA, "/")
	defer serverA.Close()

	appB := via.New(via.WithTestServer(&serverB), via.WithBackplane(shared), interval)
	via.Mount[syncOffAppPage](appB, "/")
	defer serverB.Close()

	// Register B's value cell (a reader mounts the page) so the sweep has a key
	// to reconcile, then have A write SILENTLY (no Change hint emitted).
	_ = vt.NewClient(t, serverB, "/")
	a := vt.NewClient(t, serverA, "/")
	require.Equal(t, 200, a.Action("BumpSilently").Fire())

	// B's tailer got nothing (silent suppressed the hint); the sweep must still
	// carry a fresh reader on B to the value A committed to the shared Store.
	require.Eventually(t, func() bool {
		return strings.Contains(vt.NewClient(t, serverB, "/").HTML(), `<span id="visits">1</span>`)
	}, 2*time.Second, 20*time.Millisecond,
		"the reconcile sweep must converge pod B even though no Change hint was emitted")
}

// WithReconcileInterval(0) disables the sweep — a documented mode where the
// changes feed alone carries convergence. A LOUD write (which emits a hint)
// must still converge a peer via its changes-tailer with no sweep running.
// (appCounterPage lives in stateapp_test.go: StateAppNum[int] Visits + loud
// Bump + <span id="visits"> view.)
func TestChangesFeedAloneConvergesWithReconcileDisabled(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()
	off := via.WithReconcileInterval(0)

	var serverA, serverB *httptest.Server
	appA := via.New(via.WithTestServer(&serverA), via.WithBackplane(shared), off)
	via.Mount[appCounterPage](appA, "/")
	defer serverA.Close()

	appB := via.New(via.WithTestServer(&serverB), via.WithBackplane(shared), off)
	via.Mount[appCounterPage](appB, "/")
	defer serverB.Close()

	a := vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	require.Equal(t, 200, a.Action("Bump").Fire())

	// No sweep is running; B converges purely through the changes-feed tailer.
	vt.AwaitFrame(t, framesB, 2*time.Second, `<span id="visits">1</span>`)
}

// A backplane wired via WithBackplane must be gracefully drained when the App
// shuts down — otherwise its goroutines/connections outlive the server. After
// Shutdown the caller's own reference must observe the closed state.
func TestWithBackplaneIsDrainedOnShutdown(t *testing.T) {
	t.Parallel()

	bp := via.InMemory()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithBackplane(bp))
	defer server.Close()

	require.NoError(t, app.Shutdown(context.Background()))

	_, err := bp.Append(context.Background(), "k", []byte("x"))
	assert.ErrorIs(t, err, via.ErrClosed,
		"App.Shutdown must Close the backplane wired via WithBackplane")
}

// Adding the backplane drain to Shutdown must not regress the default-app path:
// a plain via.New() (no WithBackplane) still shuts down cleanly. This guards the
// new Close() step; that the nil default actually resolves to a real InMemory
// backplane (rather than a tolerated nil) is verified once Read/Append on the
// handle exist (P1.1b) — it is not black-box observable at this slice.
func TestDefaultAppShutsDownCleanlyWithBackplaneDrain(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	defer server.Close()

	assert.NotPanics(t, func() {
		require.NoError(t, app.Shutdown(context.Background()))
	}, "a default app resolves nil to InMemory and drains it without panic")
}
