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
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cookieEchoPage struct {
	Flavor via.State[string]
}

func (p *cookieEchoPage) OnInit(ctx *via.Ctx) error {
	p.Flavor.Set(ctx, ctx.Cookie("flavor"))
	return nil
}

func (p *cookieEchoPage) View(ctx *via.Ctx) h.H { return h.Div(p.Flavor.Text()) }

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

func (s *searchPage) View(ctx *via.Ctx) h.H {
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
	Email via.Signal[string]
}

func (p *sessionProbePage) Probe(ctx *via.Ctx) error {
	if ctx.Session() != nil {
		p.Email.Set(ctx, "session-present")
	}
	return nil
}

func (p *sessionProbePage) View(*via.Ctx) h.H { return h.Div() }

func TestCtx_Session_isPopulatedOnHTTPDrivenAction(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[sessionProbePage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	require.Equal(t, http.StatusOK, tc.Action("Probe").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "session-present")
}

// Disposed flag + OnDispose hook — Ctx lifecycle

var disposed atomic.Int32

type disposable struct {
	N via.State[int]
}

func (d *disposable) OnDispose(ctx *via.Ctx) {
	disposed.Add(1)
}

func (d *disposable) View(ctx *via.Ctx) h.H { return h.Div() }

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
	ctx.PatchSignal("_connected", true)
	return nil
}

func (c *connectStateCheck) View(ctx *via.Ctx) h.H { return h.Div() }

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

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	viatest.AwaitFrame(t, frames, 2*time.Second, "_connected")

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

func (d *doneSelfCheck) View(ctx *via.Ctx) h.H { return h.Div() }

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

	_ = viatest.NewClient(t, server, "/")
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

func (d *disposedSelfCheck) View(ctx *via.Ctx) h.H { return h.Div() }

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

	_ = viatest.NewClient(t, server, "/")
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

	_ = viatest.NewClient(t, server, "/")

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
		{"Sync", func() { ctx.Sync() }},
		{"Flush", func() { ctx.Flush() }},
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

func (p *ctxScriptPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestCtx_Reload_emitsLocationReloadScript(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("DoReload").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "location.reload()")
}

func TestCtx_Toast_emitsAlertScript(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("DoToast").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `alert("saved!")`)
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

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("DoToastSpecial").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second,
		`alert("he said \"ok\\n done\"")`)
}

func TestCtx_Redirect_emitsRedirectFrame(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[ctxScriptPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, http.StatusOK, tc.Action("DoRedirect").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "/elsewhere")
}
