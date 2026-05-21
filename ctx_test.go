package via_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cookieEchoPage struct {
	Flavor via.StateTabStr
}

func (p *cookieEchoPage) OnInit(ctx *via.Ctx) error {
	p.Flavor.Write(ctx, ctx.Cookie("flavor"))
	return nil
}

func (p *cookieEchoPage) View(ctx *via.CtxR) h.H {
	return h.Div(p.Flavor.Text(ctx))
}

func TestCookie_readsValueFromRequest(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[cookieEchoPage](app, "/")
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	req.AddCookie(&http.Cookie{Name: "flavor", Value: "mint"})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "mint",
		"ctx.Cookie should read the named cookie off the in-flight request")
}

type searchPage struct {
	Q     string `query:"q"`
	Page  int    `query:"page"`
	Debug bool   `query:"debug"`
}

func (s *searchPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.Textf("q=%q", s.Q)),
		h.Span(h.Textf("page=%d", s.Page)),
		h.Span(h.Textf("debug=%t", s.Debug)),
	)
}

func TestQuery_decodesIntoTaggedFields(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[searchPage](app, "/search")
	defer server.Close()

	body := getBody(t, server, "/search?"+url.Values{
		"q":     {"hello"},
		"page":  {"3"},
		"debug": {"true"},
	}.Encode())
	assert.Contains(t, body, `q=&#34;hello&#34;`)
	assert.Contains(t, body, "page=3")
	assert.Contains(t, body, "debug=true")
}

func TestQuery_missingFieldsKeepZeroValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[searchPage](app, "/search")
	defer server.Close()

	body := getBody(t, server, "/search")
	assert.Contains(t, body, `q=&#34;&#34;`)
	assert.Contains(t, body, "page=0")
	assert.Contains(t, body, "debug=false")
}

// Ctx.Session — accessor on the live Ctx (HTTP-driven)

type sessionProbePage struct {
	Email via.SignalStr
}

func (p *sessionProbePage) Probe(ctx *via.Ctx) error {
	if ctx.Session() != nil {
		p.Email.Write(ctx, "session-present")
	}
	return nil
}

func (p *sessionProbePage) View(*via.CtxR) h.H { return h.Div() }

func TestCtx_Session_isPopulatedOnHTTPDrivenAction(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[sessionProbePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	require.Equal(t, http.StatusOK, tc.Action("Probe").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "session-present")
}

// Disposed flag + OnDispose hook — Ctx lifecycle

var disposed atomic.Int32

type disposable struct {
	N via.StateTabNum[int]
}

func (d *disposable) OnDispose(ctx *via.Ctx) {
	disposed.Add(1)
}

func (d *disposable) View(ctx *via.CtxR) h.H { return h.Div() }

var (
	disposedFalseInsideOnConnect atomic.Bool
	doneOpenInsideOnConnect      atomic.Bool
)

type connectStateCheck struct{}

func (c *connectStateCheck) OnConnect(ctx *via.Ctx) error {
	if !ctx.Disposed() {
		disposedFalseInsideOnConnect.Store(true)
	}
	select {
	case <-ctx.Done():
		// channel closed — failure, leaves doneOpenInsideOnConnect false
	default:
		doneOpenInsideOnConnect.Store(true)
	}
	// Drive a signal so the SSE drain has something to flush — that's
	// the await condition below.
	ctx.Patch.Signal("_connected", true)
	return nil
}

func (c *connectStateCheck) View(ctx *via.CtxR) h.H { return h.Div() }

func TestOnConnect_ctxIsLiveAndDoneIsOpen(t *testing.T) {
	t.Parallel()
	// Symmetric to TestDisposed_trueInsideOnDispose / TestDone_channelClosedInsideOnDispose:
	// while OnConnect runs, the ctx is fully live. Disposed must be
	// false; Done's channel must NOT be closed yet.
	disposedFalseInsideOnConnect.Store(false)
	doneOpenInsideOnConnect.Store(false)

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[connectStateCheck](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	vt.AwaitFrame(t, frames, 2*time.Second, "_connected")

	assert.True(t, disposedFalseInsideOnConnect.Load(),
		"ctx.Disposed() must be false while OnConnect runs")
	assert.True(t, doneOpenInsideOnConnect.Load(),
		"ctx.Done() must not be closed while OnConnect runs")
}

var doneChanClosedInsideOnDispose atomic.Bool

type doneSelfCheck struct{}

func (d *doneSelfCheck) OnDispose(ctx *via.Ctx) {
	select {
	case <-ctx.Done():
		doneChanClosedInsideOnDispose.Store(true)
	case <-time.After(100 * time.Millisecond):
		// Channel not closed yet — assertion below will fail.
	}
}

func (d *doneSelfCheck) View(ctx *via.CtxR) h.H { return h.Div() }

func TestDone_channelClosedInsideOnDispose(t *testing.T) {
	t.Parallel()
	// Sibling to TestDisposed_trueInsideOnDispose: disposeCtx closes
	// ctx.doneChan before invoking the user's OnDispose, so a select
	// on ctx.Done() returns immediately throughout the callback body.
	doneChanClosedInsideOnDispose.Store(false)
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[doneSelfCheck](app, "/")
	defer server.Close()

	_ = vt.NewClient(t, server, "/")
	require.NoError(t, app.Shutdown(context.Background()))
	require.Eventually(t,
		func() bool { return doneChanClosedInsideOnDispose.Load() },
		2*time.Second, 10*time.Millisecond,
		"ctx.Done() must be a closed channel by the time OnDispose runs")
}

var disposedFlagSeenInsideOnDispose atomic.Bool

type disposedSelfCheck struct{}

func (d *disposedSelfCheck) OnDispose(ctx *via.Ctx) {
	disposedFlagSeenInsideOnDispose.Store(ctx.Disposed())
}

func (d *disposedSelfCheck) View(ctx *via.CtxR) h.H { return h.Div() }

func TestDisposed_trueInsideOnDispose(t *testing.T) {
	t.Parallel()
	// disposeCtx flips ctx.disposed and closes doneChan before invoking
	// the user's OnDispose. The user contract is "ctx.Disposed() returns
	// true throughout the OnDispose body" — pin it.
	disposedFlagSeenInsideOnDispose.Store(false)
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[disposedSelfCheck](app, "/")
	defer server.Close()

	_ = vt.NewClient(t, server, "/")
	require.NoError(t, app.Shutdown(context.Background()))
	require.Eventually(t,
		func() bool { return disposedFlagSeenInsideOnDispose.Load() },
		2*time.Second, 10*time.Millisecond,
		"ctx.Disposed() must already be true by the time OnDispose runs")
}

func TestDispose_runsOnAppShutdown(t *testing.T) {
	t.Parallel()

	disposed.Store(0)
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[disposable](app, "/")
	defer server.Close()

	_ = vt.NewClient(t, server, "/")

	require.NoError(t, app.Shutdown(context.Background()))
	require.Eventually(t, func() bool { return disposed.Load() == 1 },
		2*time.Second, 10*time.Millisecond,
		"OnDispose not called after Shutdown")
}

func TestDisposed_trueOnNilReceiver(t *testing.T) {
	t.Parallel()
	// A nil *Ctx is by definition no longer live — Disposed returns true
	// so callers can short-circuit safely instead of dereferencing.
	var ctx *via.Ctx
	assert.True(t, ctx.Disposed())
}

func TestCtx_coreHelpersTolerateNilReceiver(t *testing.T) {
	t.Parallel()
	// Sibling to TestCtx_pushHelpersToleratesNilReceiver — covers the
	// nil-receiver guards in ctx.go itself (not push.go).
	var ctx *via.Ctx
	cases := []struct {
		name string
		fn   func()
	}{
		{"SyncNow", func() { ctx.SyncNow() }},
		{"SyncOff", func() { ctx.SyncOff() }},
		{"SetCookie", func() { ctx.SetCookie(nil) }},
		{"DelCookie", func() { ctx.DelCookie("") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.NotPanics(t, c.fn)
		})
	}
}

// Reload / Toast / Redirect — ctx imperative helpers emit SSE frames

type ctxScriptPage struct{}

func (p *ctxScriptPage) DoReload(ctx *via.Ctx) error {
	ctx.Reload()
	return nil
}

func (p *ctxScriptPage) DoToast(ctx *via.Ctx) error {
	ctx.Toast("saved!")
	return nil
}

func (p *ctxScriptPage) DoToastSpecial(ctx *via.Ctx) error {
	// Embedded quotes, newline, and a backslash exercise escape paths
	// where Go's %q diverges from JSON / JS string literal syntax.
	ctx.Toast(`he said "ok\n done"`)
	return nil
}

func (p *ctxScriptPage) DoRedirect(ctx *via.Ctx) error {
	ctx.Redirect("/elsewhere")
	return nil
}

func (p *ctxScriptPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestCtx_Reload_emitsLocationReloadScript(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("DoReload").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "location.reload()")
}

func TestCtx_Toast_emitsAlertScript(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("DoToast").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `alert("saved!")`)
}

func TestCtx_Toast_JSONEncodesSpecialChars(t *testing.T) {
	t.Parallel()
	// JSON encodes the inner quote as \" and the newline as \n — both
	// match exactly how a JS engine parses a string literal. Catches a
	// regression where Toast started using fmt.Sprintf("alert(%q)") which
	// would produce Go-quote escaping incompatible with JS.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("DoToastSpecial").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second,
		`alert("he said \"ok\\n done\"")`)
}

func TestCtx_Redirect_emitsRedirectFrame(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("DoRedirect").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "/elsewhere")
}

// SyncOff — action-scoped publish suppression.

type syncOffPage struct {
	N     via.StateTabNum[int]
	Theme via.StateSessStr
}

func (p *syncOffPage) SilentWrite(ctx *via.Ctx) error {
	ctx.SyncOff()
	p.N.Write(ctx, 9)
	p.Theme.Op(ctx).To("midnight")
	return nil
}

func (p *syncOffPage) LoudAfter(ctx *via.Ctx) error {
	p.N.Write(ctx, p.N.Read(ctx))
	return nil
}

func (p *syncOffPage) NoOp(ctx *via.Ctx) error { return nil }

func (p *syncOffPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.ID("n"), p.N.Text(ctx)),
		h.Span(h.ID("theme"), p.Theme.Text(ctx)),
	)
}

func TestSyncOff_skipsEndOfActionFlush(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncOffPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("SilentWrite").Fire())

	select {
	case frame := <-frames:
		assert.Failf(t, "Silent action must not flush",
			"unexpected SSE frame %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

type syncOffAppPage struct {
	Visits via.StateAppNum[int]
}

func (p *syncOffAppPage) BumpSilently(ctx *via.Ctx) error {
	ctx.SyncOff()
	_ = p.Visits.Update(ctx, func(n int) (int, error) { return n + 1, nil })
	return nil
}

func (p *syncOffAppPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("visits"), p.Visits.Text(ctx)))
}

func TestSyncOff_skipsStateAppBroadcastAcrossSessions(t *testing.T) {
	t.Parallel()
	// StateApp fans out across every session, not just same-session
	// siblings. The sibling-tab test (same session via Fork) doesn't
	// cover this fan-out scope, so we exercise it directly with two
	// distinct sessions.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncOffAppPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	b := vt.NewClient(t, server, "/") // different session

	framesB, cancelB := b.SSE()
	defer cancelB()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, a.Action("BumpSilently").Fire())

	select {
	case frame := <-framesB:
		assert.Failf(t, "SyncOff must suppress StateApp cross-session broadcast",
			"unrelated session got SSE frame %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSyncOff_skipsBroadcastToSiblingTabs(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncOffPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	b := a.Fork("/")

	framesB, cancelB := b.SSE()
	defer cancelB()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, a.Action("SilentWrite").Fire())

	select {
	case frame := <-framesB:
		assert.Failf(t, "Silent action must not fan out",
			"sibling tab got SSE frame %q", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSyncOff_writesPersistAndSurfaceOnNextLoudAction(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncOffPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("SilentWrite").Fire())
	// Loud action re-renders; both N and Theme should reflect prior silent writes.
	require.Equal(t, http.StatusOK, tc.Action("LoudAfter").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second,
		`<span id="n">9</span>`, `<span id="theme">midnight</span>`)
}

func TestSyncOff_dirtyBitsDoNotLeakIntoNextActionFlush(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncOffPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// Silent action accumulates dirty bits but skips its own flush.
	// If discardDirty isn't called, the next handler's deferred flush
	// would surface the silent writes (the values persist in their
	// stores) — which would defeat the whole "publish nothing" contract.
	require.Equal(t, http.StatusOK, tc.Action("SilentWrite").Fire())
	require.Equal(t, http.StatusOK, tc.Action("NoOp").Fire())

	select {
	case frame := <-frames:
		assert.Failf(t, "silent dirty bits leaked into next action's flush",
			"got SSE frame %q after a NoOp following a Silent write", frame)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSyncOff_nilCtxIsANoOp(t *testing.T) {
	t.Parallel()
	var ctx *via.Ctx
	require.NotPanics(t, func() { ctx.SyncOff() })
}

type syncOffStreamPage struct {
	N      via.StateTabNum[int]
	silent atomic.Bool
}

func (p *syncOffStreamPage) OnConnect(ctx *via.Ctx) error {
	via.Stream(ctx, 10*time.Millisecond, func(c *via.Ctx, _ time.Time) {
		if p.silent.Load() {
			c.SyncOff()
		}
		_ = p.N.Update(c, func(n int) (int, error) { return n + 1, nil })
	})
	return nil
}

func (p *syncOffStreamPage) GoSilent(ctx *via.Ctx) error {
	p.silent.Store(true)
	return nil
}

func (p *syncOffStreamPage) GoLoud(ctx *via.Ctx) error {
	p.silent.Store(false)
	return nil
}

func (p *syncOffStreamPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("n"), p.N.Text(ctx)))
}

func TestSyncOff_suppressesStreamTickPublish(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncOffStreamPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	vt.AwaitFrame(t, frames, 2*time.Second, `<span id="n">1</span>`)

	require.Equal(t, http.StatusOK, tc.Action("GoSilent").Fire())
	drainFrames(frames, 50*time.Millisecond)

	select {
	case frame := <-frames:
		assert.Failf(t, "Silent stream tick must not flush",
			"unexpected SSE frame %q", frame)
	case <-time.After(150 * time.Millisecond):
	}
}

// drainFrames consumes any frames sitting in the channel for d.
func drainFrames(frames <-chan string, d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case <-frames:
		case <-deadline:
			return
		}
	}
}

type syncOffRacePage struct {
	N via.StateAppNum[int]
}

func (p *syncOffRacePage) View(ctx *via.CtxR) h.H { return h.Div(p.N.Text(ctx)) }

func (p *syncOffRacePage) Spawn(ctx *via.Ctx) error {
	go func() {
		for i := 0; i < 100; i++ {
			_ = p.N.Update(ctx, func(n int) (int, error) { return n + 1, nil })
			time.Sleep(time.Microsecond)
		}
	}()
	return nil
}

func (p *syncOffRacePage) Toggle(ctx *via.Ctx) error {
	ctx.SyncOff()
	time.Sleep(time.Microsecond)
	return nil
}

func TestSyncOff_doesNotRaceWithRawGoroutineUpdate(t *testing.T) {
	t.Parallel()
	// User goroutine driving StateApp.Update → broadcastRender reads
	// ctx.silent without holding actionMu, while a parallel action
	// resets the flag at entry. Plain-bool implementation tripped -race;
	// atomic.Bool keeps the contract goroutine-safe.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncOffRacePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	_, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("Spawn").Fire())

	for i := 0; i < 50; i++ {
		require.Equal(t, http.StatusOK, tc.Action("Toggle").Fire())
	}
	time.Sleep(50 * time.Millisecond)
}

type syncOffPanicPage struct {
	N via.StateTabNum[int]
}

func (p *syncOffPanicPage) BoomSilently(ctx *via.Ctx) error {
	ctx.SyncOff()
	p.N.Write(ctx, 42)
	panic("boom-while-silent")
}

func (p *syncOffPanicPage) View(ctx *via.CtxR) h.H { return h.Div(p.N.Text(ctx)) }

func TestSyncOff_panicErrorToastStillReachesClient(t *testing.T) {
	t.Parallel()
	// dispatchActionError enqueues a script (alert) directly onto the
	// patch queue. SyncOff suppresses dirty-bit flushes but must not
	// swallow explicit publish primitives — otherwise a panicking
	// silent action would fail without any user-visible signal.
	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithLogLevel(via.LogError),
	)
	via.Mount[syncOffPanicPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("BoomSilently").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "Something went wrong")
}

type syncOffExplicitPage struct{}

func (p *syncOffExplicitPage) View(ctx *via.CtxR) h.H { return h.Div() }

func (p *syncOffExplicitPage) SilentToast(ctx *via.Ctx) error {
	ctx.SyncOff()
	ctx.Toast("ping")
	return nil
}

func (p *syncOffExplicitPage) SilentPatchSignal(ctx *via.Ctx) error {
	ctx.SyncOff()
	ctx.Patch.Signal("_marker", "hello")
	return nil
}

func (p *syncOffExplicitPage) SilentSyncElements(ctx *via.Ctx) error {
	ctx.SyncOff()
	ctx.Patch.Elements(h.Div(h.ID("marker"), h.Text("morphed")))
	return nil
}

func TestSyncOff_doesNotSuppressExplicitPublishPrimitives(t *testing.T) {
	t.Parallel()
	// SyncOff gates dirty-bit-driven publishing. PatchSignal /
	// SyncElements / Toast write directly onto the patch queue and
	// must surface even while silent — they're how user code signals
	// "publish this regardless of pending dirty bits".
	cases := []struct {
		name   string
		action string
		expect string
	}{
		{"Toast", "SilentToast", `alert("ping")`},
		{"PatchSignal", "SilentPatchSignal", "hello"},
		{"SyncElements", "SilentSyncElements", `id="marker"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var server *httptest.Server
			app := via.New(via.WithTestServer(&server))
			via.Mount[syncOffExplicitPage](app, "/")
			defer server.Close()

			tc := vt.NewClient(t, server, "/")
			frames, cancel := tc.SSE()
			defer cancel()
			time.Sleep(20 * time.Millisecond)

			require.Equal(t, http.StatusOK, tc.Action(c.action).Fire())
			vt.AwaitFrame(t, frames, 2*time.Second, c.expect)
		})
	}
}
