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

type cookieWritePage struct {
	Seen via.StateTabStr
}

func (p *cookieWritePage) SetPref(ctx *via.Ctx) error {
	ctx.SetCookie(&http.Cookie{Name: "pref", Value: "dark", Path: "/"})
	return nil
}

func (p *cookieWritePage) Forget(ctx *via.Ctx) error {
	ctx.DelCookie("pref")
	return nil
}

func (p *cookieWritePage) Show(ctx *via.Ctx) error {
	p.Seen.Write(ctx, "pref=["+ctx.Cookie("pref")+"]")
	return nil
}

func (p *cookieWritePage) View(ctx *via.CtxR) h.H {
	return h.Div(p.Seen.Text(ctx))
}

func TestSetCookie_writesCookieReadableOnNextAction(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[cookieWritePage](app, "/")
	defer server.Close()

	// vt's client shares a cookie jar: a cookie SetCookie writes on one
	// action's response is sent back on the next request, where ctx.Cookie
	// reads it.
	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("SetPref").Fire())
	require.Equal(t, 200, tc.Action("Show").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "pref=[dark]")
}

func TestDelCookie_clearsCookieForNextAction(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[cookieWritePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("SetPref").Fire())
	require.Equal(t, 200, tc.Action("Forget").Fire())
	require.Equal(t, 200, tc.Action("Show").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "pref=[]")
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

var sweepDisposed atomic.Bool

type sweepDisposePage struct{}

func (p *sweepDisposePage) OnDispose(ctx *via.Ctx) { sweepDisposed.Store(true) }

func (p *sweepDisposePage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestContextSweep_disposesIdleTabAfterTTL(t *testing.T) {
	t.Parallel()
	sweepDisposed.Store(false)

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithContextTTL(40*time.Millisecond))
	via.Mount[sweepDisposePage](app, "/")
	defer server.Close()

	// A plain GET registers an idle Ctx; with no SSE or action to touch it,
	// the background TTL sweep (every contextTTL/2) must remove and dispose
	// it once it passes the idle cutoff.
	_ = vt.NewClient(t, server, "/")

	require.Eventually(t, sweepDisposed.Load, 3*time.Second, 10*time.Millisecond,
		"an idle tab's Ctx must be swept (OnDispose run) after the context TTL")
}

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

func (p *ctxScriptPage) DoToastHTML(ctx *via.Ctx) error {
	ctx.Toast(`<img src=x onerror="boom()">`)
	return nil
}

func (p *ctxScriptPage) DoToastScriptBreakout(ctx *via.Ctx) error {
	ctx.Toast(`</script><img src=x onerror=boom()>`)
	return nil
}

func (p *ctxScriptPage) DoRedirect(ctx *via.Ctx) error {
	ctx.Redirect("/elsewhere")
	return nil
}

type redirectPage struct {
	URL via.SignalStr `via:"url"`
}

func (p *redirectPage) Go(ctx *via.Ctx) error {
	ctx.Redirect(p.URL.Read(ctx))
	return nil
}

func (p *redirectPage) Ack(ctx *via.Ctx) error {
	ctx.Toast("ack")
	return nil
}

func (p *redirectPage) View(ctx *via.CtxR) h.H { return h.Div() }

func (p *ctxScriptPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestCtx_Reload_emitsLocationReloadScript(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("DoReload").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "location.reload()")
}

func TestCtx_Toast_rendersStyledToastNotAlert(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("DoToast").Fire())
	frame := vt.AwaitFrame(t, frames, 2*time.Second, "via-toast-root", "saved!")
	assert.NotContains(t, frame, "alert(",
		"default toast must not fall back to a blocking window.alert")
}

func TestCtx_Toast_injectsMessageAsJSStringLiteral(t *testing.T) {
	t.Parallel()
	// The message rides into the toast script as a JSON-encoded literal:
	// the inner quote becomes \" and the newline \n, matching how a JS
	// engine parses a string literal. Catches a regression where Toast
	// interpolated the raw message into the script and let it break out
	// of the string context.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("DoToastSpecial").Fire())
	frame := vt.AwaitFrame(t, frames, 2*time.Second, "via-toast-root",
		`"he said \"ok\\n done\""`)
	assert.NotContains(t, frame, "alert(")
}

func TestCtx_Toast_escapesHTMLMarkupInMessage(t *testing.T) {
	t.Parallel()
	// json.Marshal HTML-escapes the angle brackets and ampersand to their
	// unicode-escaped forms, so an HTML payload reaches the wire as an
	// inert JSON string literal, never as live markup. The test inspects
	// the emitted frame (not the DOM): a SetEscapeHTML(false) regression
	// would surface a literal "<img" here and fail the assertion.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("DoToastHTML").Fire())
	frame := vt.AwaitFrame(t, frames, 2*time.Second, "via-toast-root", "boom()")
	assert.NotContains(t, frame, "<img",
		"user HTML must be escaped, never emitted as live markup")
}

func TestCtx_Toast_cannotBreakOutOfTheScriptElement(t *testing.T) {
	t.Parallel()
	// datastar delivers the toast snippet inside a <script>…</script>
	// element, so a message carrying a literal </script> is the
	// element-breakout vector. json.Marshal escapes < and >, so the
	// sequence can't appear verbatim and the message stays inside the
	// script.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("DoToastScriptBreakout").Fire())
	frame := vt.AwaitFrame(t, frames, 2*time.Second, "via-toast-root", "boom()")
	assert.NotContains(t, frame, "</script><img",
		"a </script> in the message must not break out of the datastar script element")
}

func TestCtx_Redirect_emitsRedirectFrame(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, tc.Action("DoRedirect").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "/elsewhere")
}

func TestCtx_Redirect_allowsSafeURLs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		url  string
	}{
		{"absolute path", "/elsewhere"},
		{"relative path", "relative/path"},
		{"http", "http://example.com/x"},
		{"https", "https://example.com/x"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var server *httptest.Server
			app := via.New(via.WithTestServer(&server))
			via.Mount[redirectPage](app, "/")
			defer server.Close()

			tc := vt.NewClient(t, server, "/")
			frames, cancel := tc.SSEReady()
			defer cancel()

			require.Equal(t, http.StatusOK,
				tc.Action("Go").WithSignal("url", tt.url).Fire())
			vt.AwaitFrame(t, frames, 2*time.Second,
				"window.location.href", tt.url)
		})
	}
}

func TestCtx_Redirect_dropsDangerousURLs(t *testing.T) {
	t.Parallel()
	// Open-redirect / XSS vectors must not reach the client. The Redirect
	// patch is dropped; a subsequent observable patch (Toast) confirms the
	// SSE stream is still alive and that the dangerous URL never appears
	// in any frame that did arrive.
	cases := []struct {
		name string
		url  string
	}{
		{"javascript scheme", "javascript:alert(1)"},
		{"protocol relative", "//evil.example/path"},
		{"data scheme", "data:text/html,<script>alert(1)</script>"},
		{"vbscript scheme", "vbscript:msgbox"},
		{"uppercase javascript", "JavaScript:alert(1)"},
		{"whitespace javascript", " javascript:alert(1)"},
		{"backslash protocol relative", `\\evil.example/path`},
		{"whitespace and control chars only", "   \t  "},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var server *httptest.Server
			app := via.New(via.WithTestServer(&server))
			via.Mount[redirectPage](app, "/")
			defer server.Close()

			tc := vt.NewClient(t, server, "/")
			frames, cancel := tc.SSEReady()
			defer cancel()

			require.Equal(t, http.StatusOK,
				tc.Action("Go").WithSignal("url", tt.url).Fire())
			require.Equal(t, http.StatusOK, tc.Action("Ack").Fire())
			body := vt.AwaitFrame(t, frames, 2*time.Second, "via-toast-root", "ack")
			assert.NotContains(t, body, "window.location.href",
				"dangerous URL must not produce a redirect frame")
			assert.NotContains(t, body, tt.url,
				"dangerous URL must not appear in any SSE frame")
		})
	}
}

// SyncOff — action-scoped publish suppression.

type syncOffPage struct {
	N     via.StateTabNum[int]
	Theme via.StateSessStr
}

func (p *syncOffPage) SilentWrite(ctx *via.Ctx) error {
	ctx.SyncOff()
	p.N.Write(ctx, 9)
	_ = p.Theme.Update(ctx, func(string) (string, error) { return "midnight", nil })
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
	frames, cancel := tc.SSEReady()
	defer cancel()

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

	framesB, cancelB := b.SSEReady()
	defer cancelB()

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

	framesB, cancelB := b.SSEReady()
	defer cancelB()

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
	frames, cancel := tc.SSEReady()
	defer cancel()

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
	frames, cancel := tc.SSEReady()
	defer cancel()

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
	_, cancel := tc.SSEReady()
	defer cancel()

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
	// dispatchActionError enqueues a toast script directly onto the
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
	frames, cancel := tc.SSEReady()
	defer cancel()

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
		{"Toast", "SilentToast", "ping"},
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
			frames, cancel := tc.SSEReady()
			defer cancel()

			require.Equal(t, http.StatusOK, tc.Action(c.action).Fire())
			vt.AwaitFrame(t, frames, 2*time.Second, c.expect)
		})
	}
}
