package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type queueOrderPage struct {
	N via.StateTabNum[int]
}

func (p *queueOrderPage) Bump(ctx *via.Ctx) error {
	return p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *queueOrderPage) BumpAndOverride(ctx *via.Ctx) error {
	if err := p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil }); err != nil {
		return err
	}
	ctx.Patch.Elements(h.Div(h.ID("n"), h.Text("OVERRIDE")))
	return nil
}

func (p *queueOrderPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("n"), p.N.Text(ctx))
}

// A tab whose SSE is down (hidden tab, transient drop) keeps acting via
// POSTs; every flush re-renders the view into the patch queue. On
// reconnect the drained frame must leave the client on the NEWEST
// render — datastar applies same-id patches last-wins, so a stale
// fragment surviving after the fresh one silently rewinds the UI
// (observed live: tab stuck on the first of five increments).
func TestReconnectAfterOfflineActionsShowsNewestState(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[queueOrderPage](app, "/q")
	defer server.Close()

	tc := vt.NewClient(t, server, "/q")
	// Three state-mutating actions with no SSE stream open: each flush
	// queues a fresh view render while nothing drains.
	for i := 0; i < 3; i++ {
		require.Equal(t, http.StatusOK, tc.Action("Bump").Fire())
	}

	frames, cancel := tc.SSE()
	defer cancel()
	body := vt.AwaitFrame(t, frames, 2*time.Second, ": ready")

	assert.Contains(t, body, ">3<",
		"reconnect drain must carry the newest render")
	assert.NotContains(t, body, ">1<",
		"stale renders must not survive in the drained frame — last-wins morph would rewind the UI to them")
	assert.NotContains(t, body, ">2<",
		"stale renders must not survive in the drained frame — last-wins morph would rewind the UI to them")
}

// A user-explicit Patch.Elements targeting an id the auto re-render also
// ships must stay authoritative: datastar applies patches in document
// order, so the explicit fragment has to come AFTER the auto render in
// the wire frame.
func TestExplicitElementPatchOverridesAutoRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[queueOrderPage](app, "/q")
	defer server.Close()

	tc := vt.NewClient(t, server, "/q")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("BumpAndOverride").Fire())
	body := vt.AwaitFrame(t, frames, 2*time.Second, ">1<", "OVERRIDE")

	auto := strings.Index(body, ">1<")
	override := strings.Index(body, "OVERRIDE")
	require.GreaterOrEqual(t, auto, 0, "auto render must be in the frame")
	assert.Greater(t, override, auto,
		"explicit patch must come after the auto render so last-wins keeps it authoritative")
	// One action's patches must drain as ONE element-patch event. If the
	// mid-action Patch.Elements notify triggers an early drain, the
	// override ships in its own frame BEFORE the end-of-action auto render
	// — two events, with the auto render last, silently rewinding the UI
	// off the override under datastar's last-wins-per-id morph.
	assert.Equal(t, 1, strings.Count(body, "datastar-patch-elements"),
		"an action's auto render and explicit patch must ship in a single element-patch event")
}

// Explicit patches from separate offline actions are independent pushes
// (often to different targets) — both must survive the reconnect drain,
// in the order they were queued.
func TestExplicitPatchesFromSeparateActionsAllSurviveReconnect(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[explicitQueuePage](app, "/e")
	defer server.Close()

	tc := vt.NewClient(t, server, "/e")
	require.Equal(t, http.StatusOK, tc.Action("PushA").Fire())
	require.Equal(t, http.StatusOK, tc.Action("PushB").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	body := vt.AwaitFrame(t, frames, 2*time.Second, ": ready")

	a := strings.Index(body, "PATCH-A")
	b := strings.Index(body, "PATCH-B")
	require.GreaterOrEqual(t, a, 0, "first explicit patch must survive")
	require.GreaterOrEqual(t, b, 0, "second explicit patch must survive")
	assert.Greater(t, b, a, "explicit patches must drain in queue order")
}

// A view that panics once N reaches the panic threshold. Used to prove a
// later panicking re-render does not erase a previously queued good render.
type panicRenderPage struct {
	N via.StateTabNum[int]
}

func (p *panicRenderPage) Bump(ctx *via.Ctx) error {
	return p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
}

func (p *panicRenderPage) View(ctx *via.CtxR) h.H {
	if p.N.Read(ctx) >= 2 {
		panic("boom")
	}
	return h.Div(h.ID("n"), p.N.Text(ctx))
}

// A disconnected tab queues a good auto-render, then a later action's
// re-render panics (yielding an empty fragment). The empty fragment must
// NOT clobber the queued good render: on reconnect the client must still
// receive the last good view, not an empty frame.
func TestPanickingRenderDoesNotEraseQueuedGoodRender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[panicRenderPage](app, "/p")
	defer server.Close()

	tc := vt.NewClient(t, server, "/p")
	// N=1: good render queued. N=2: view panics, empty fragment — must not
	// erase the queued ">1<".
	require.Equal(t, http.StatusOK, tc.Action("Bump").Fire())
	require.Equal(t, http.StatusOK, tc.Action("Bump").Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	body := vt.AwaitFrame(t, frames, 2*time.Second, ": ready")

	assert.Contains(t, body, ">1<",
		"a later panicking render must not erase the last good queued render")
}

type explicitQueuePage struct{}

func (p *explicitQueuePage) PushA(ctx *via.Ctx) {
	ctx.Patch.Elements(h.Div(h.ID("a"), h.Text("PATCH-A")))
}

func (p *explicitQueuePage) PushB(ctx *via.Ctx) {
	ctx.Patch.Elements(h.Div(h.ID("b"), h.Text("PATCH-B")))
}

func (p *explicitQueuePage) PushSilent(ctx *via.Ctx) {
	ctx.SyncOff()
	ctx.Patch.Elements(h.Div(h.ID("a"), h.Text("PATCH-A")))
}

func (p *explicitQueuePage) View(ctx *via.CtxR) h.H { return h.Div(h.ID("root")) }

// An action that pushes an explicit patch but mutates no State queues no
// auto render, so the end-of-action flush renders nothing. The explicit
// push is then the only thing that can wake the SSE goroutine on a live
// stream — if an action that holds wakes until it returns fails to
// release that wake when there's no render, the push never reaches the
// tab.
func TestExplicitOnlyActionStillReachesLiveStream(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[explicitQueuePage](app, "/e")
	defer server.Close()

	tc := vt.NewClient(t, server, "/e")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("PushA").Fire())
	body := vt.AwaitFrame(t, frames, 2*time.Second, "PATCH-A")
	assert.Equal(t, 1, strings.Count(body, "datastar-patch-elements"),
		"the explicit push must drain in one element-patch event")
}

// SyncOff suppresses the dirty-bit re-render but NOT explicit Patch.Elements
// pushes (so a recovery toast still reaches the user on a silent action).
// The silent branch skips flushDirty entirely, so the held explicit-push
// wake must still be released at action end or the push is lost.
func TestSilentActionStillShipsExplicitPatch(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[explicitQueuePage](app, "/e")
	defer server.Close()

	tc := vt.NewClient(t, server, "/e")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("PushSilent").Fire())
	body := vt.AwaitFrame(t, frames, 2*time.Second, "PATCH-A")
	assert.Contains(t, body, "PATCH-A",
		"explicit pushes survive SyncOff even though the auto render is suppressed")
}
