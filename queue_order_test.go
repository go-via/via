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
	body := vt.AwaitFrame(t, frames, 2*time.Second, "OVERRIDE")

	auto := strings.Index(body, ">1<")
	override := strings.Index(body, "OVERRIDE")
	require.GreaterOrEqual(t, auto, 0, "auto render must be in the frame")
	assert.Greater(t, override, auto,
		"explicit patch must come after the auto render so last-wins keeps it authoritative")
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

func (p *explicitQueuePage) View(ctx *via.CtxR) h.H { return h.Div(h.ID("root")) }
