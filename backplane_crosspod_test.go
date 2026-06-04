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
