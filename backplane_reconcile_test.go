package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

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
