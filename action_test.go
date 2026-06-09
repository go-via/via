package via_test

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/spec"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type counterPage struct {
	Hits via.StateTabNum[int]
	Step via.SignalNum[int] `via:"step,init=1"`
}

func (c *counterPage) Inc(ctx *via.Ctx) error {
	c.Hits.Write(ctx, c.Hits.Read(ctx)+c.Step.Read(ctx))
	return nil
}

func (c *counterPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Button(h.Text("+"), on.Click(c.Inc)),
		c.Hits.Text(ctx),
	)
}

func TestAction_methodNameAppearsInOnClickPost(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[counterPage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, `@post(&#39;/_action/Inc&#39;)`,
		"on.Click(c.Inc) must render @post('/_action/Inc')")
}

func TestAction_unknownMethodReturns404(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[counterPage](app, "/")

	resp, err := server.Client().Post(server.URL+"/_action/Nope", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestMethodName_resolvesBoundMethod doubles as a Go-runtime canary:
// spec.MethodName recovers a method name by stripping the "-fm"
// trampoline suffix that the Go runtime emits for bound method values
// (e.g. "pkg.(*counterPage).Inc-fm"). The "-fm" suffix is a runtime
// internal, not a language contract — a Go release that changes the
// trampoline naming would silently break every `on.Click(c.Inc)` call
// site in via and downstream apps. If this test ever starts failing
// after a Go upgrade, fix MethodName before bumping the toolchain.
func TestMethodName_resolvesBoundMethod(t *testing.T) {
	t.Parallel()

	c := &counterPage{}
	assert.Equal(t, "Inc", spec.MethodName(c.Inc))
}

func TestMethodName_returnsEmptyForAnonymousFunction(t *testing.T) {
	t.Parallel()
	// Anonymous closures have no "-fm" suffix; MethodName returns "".
	// The on/* helpers turn that empty string into a panic so misuse
	// is loud — see TestClick_panicsOnAnonymousFunction.
	assert.Equal(t, "", spec.MethodName(func() {}))
}

func TestMethodName_returnsEmptyForTopLevelFunction(t *testing.T) {
	t.Parallel()
	// Package-level funcs (no receiver) have no "-fm" suffix either, so
	// MethodName must reject them just like anonymous closures.
	assert.Equal(t, "", spec.MethodName(topLevelHandler))
}

func TestMethodName_returnsEmptyForNil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", spec.MethodName(nil))
}

func topLevelHandler(ctx *via.Ctx) error { return nil }

func TestMethodName_returnsSameStringForSameMethod(t *testing.T) {
	t.Parallel()

	// Two distinct *counterPage instances → same method PC → same
	// resolved name. Catches a regression in the PC-keyed cache where
	// e.g. caching by closure address (changes per instance) instead of
	// PC would silently re-parse.
	a := &counterPage{}
	b := &counterPage{}
	assert.Equal(t, spec.MethodName(a.Inc), spec.MethodName(b.Inc))
	assert.Equal(t, "Inc", spec.MethodName(b.Inc))
}

type erroringActionPage struct{}

func (p *erroringActionPage) Save(ctx *via.Ctx) error {
	return assertSaveErr("validation: email required")
}

func (p *erroringActionPage) View(ctx *via.CtxR) h.H { return h.Div() }

type assertSaveErr string

func (e assertSaveErr) Error() string { return string(e) }

func TestAction_defaultErrorPathToastsTheBrowser(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[erroringActionPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	require.Equal(t, 200, tc.Action("Save").Fire())

	// A returned error surfaces its message through the default toast,
	// which arrives in the SSE stream as a script-patch event.
	got := vt.AwaitFrame(t, frames, 2*time.Second,
		"via-toast-root", "validation: email required")
	assert.NotContains(t, got, "alert(",
		"default error surface must be a styled toast, not a blocking alert")
}

type customErrPage struct{}

func (p *customErrPage) Save(ctx *via.Ctx) error {
	return assertSaveErr("nope")
}

func (p *customErrPage) View(ctx *via.CtxR) h.H { return h.Div() }

type panicStringPage struct{}

func (p *panicStringPage) Crash(ctx *via.Ctx) error {
	panic("internal database connection string: secret-leaks-here")
}

func (p *panicStringPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestAction_defaultPanicToastHidesInternalMessage(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[panicStringPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	require.Equal(t, 200, tc.Action("Crash").Fire())

	got := vt.AwaitFrame(t, frames, 2*time.Second,
		"via-toast-root", "Something went wrong")
	assert.NotContains(t, got, "secret-leaks-here",
		"default panic toast must not leak the internal panic message")
}

type panicTypedErr struct {
	Code string
}

func (e *panicTypedErr) Error() string { return e.Code }

type panicTypedPage struct{}

func (p *panicTypedPage) Boom(ctx *via.Ctx) error {
	panic(&panicTypedErr{Code: "E_TYPED"})
}

func (p *panicTypedPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestAction_panicWithTypedErrorPreservesType(t *testing.T) {
	t.Parallel()

	var got error
	app := via.New(
		via.WithActionErrorHandler(func(ctx *via.Ctx, err error) {
			got = err
		}),
	)
	server := vt.Serve(t, app)
	via.Mount[panicTypedPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Boom").Fire())

	require.NotNil(t, got)
	te, ok := got.(*panicTypedErr)
	require.True(t, ok, "panic with typed *panicTypedErr should be passed through to the handler verbatim, got %T", got)
	assert.Equal(t, "E_TYPED", te.Code)
}

func TestAction_WithActionErrorHandler_replacesDefaultAlert(t *testing.T) {
	t.Parallel()

	var seenErr atomic.Pointer[string]
	app := via.New(
		via.WithActionErrorHandler(func(ctx *via.Ctx, err error) {
			s := err.Error()
			seenErr.Store(&s)
		}),
	)
	server := vt.Serve(t, app)
	via.Mount[customErrPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Save").Fire())

	got := seenErr.Load()
	require.NotNil(t, got, "WithActionErrorHandler should fire on errored action")
	assert.Equal(t, "nope", *got)
}

// Per-Ctx serialization

type serialPage struct {
	N via.StateTabNum[int]
}

// Bump is intentionally non-atomic on N.Get/N.Set so the only thing
// keeping a parallel race from corrupting it is the runtime's per-Ctx
// action serialization.
func (p *serialPage) Bump(ctx *via.Ctx) error {
	cur := p.N.Read(ctx)
	p.N.Write(ctx, cur+1)
	return nil
}

func (p *serialPage) View(ctx *via.CtxR) h.H {
	return h.Div(p.N.Text(ctx), h.Button(h.Text("+"), on.Click(p.Bump)))
}

func TestAction_concurrentPOSTsAreSerializedPerCtx(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[serialPage](app, "/")

	tc := vt.NewClient(t, server, "/")

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			tc.Action("Bump").Fire()
		}()
	}
	wg.Wait()

	frames, cancel := tc.SSE()
	defer cancel()

	tc.Action("Bump").Fire() // N+1 increments by now

	// After 51 serialized increments the rendered count must be 51 — if
	// the per-Ctx mutex were broken, parallel Get/Set would lose updates
	// and we'd see a number lower than 51.
	vt.AwaitFrame(t, frames, 5*time.Second, "<div>51")
}

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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[queueOrderPage](app, "/q")

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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[queueOrderPage](app, "/q")

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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[explicitQueuePage](app, "/e")

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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[panicRenderPage](app, "/p")

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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[explicitQueuePage](app, "/e")

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

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[explicitQueuePage](app, "/e")

	tc := vt.NewClient(t, server, "/e")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("PushSilent").Fire())
	body := vt.AwaitFrame(t, frames, 2*time.Second, "PATCH-A")
	assert.Contains(t, body, "PATCH-A",
		"explicit pushes survive SyncOff even though the auto render is suppressed")
}
