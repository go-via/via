package via_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Disposed flag + OnDispose hook

var disposed atomic.Int32

type disposable struct {
	N via.State[int]
}

func (d *disposable) OnDispose(ctx *via.Ctx) {
	disposed.Add(1)
}

func (d *disposable) View(ctx *via.Ctx) h.H { return h.Div() }

type disposedFlagPage struct{}

func (p *disposedFlagPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestDisposed_falseWhileLive(t *testing.T) {
	t.Parallel()

	c := &disposedFlagPage{}
	ctx := viatest.NewCtx(t, c)
	require.False(t, ctx.Disposed(),
		"a freshly-bound Ctx should not be marked disposed")
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

// SyncElements / PatchSignal / ExecScriptf — push helpers

type syncPage struct{}

func (p *syncPage) PushList(ctx *via.Ctx) error {
	ctx.SyncElements(
		h.Ul(h.ID("results"),
			h.Li(h.Text("first")),
			h.Li(h.Text("second")),
		),
	)
	return nil
}

func (p *syncPage) Toast(ctx *via.Ctx) error {
	ctx.ExecScriptf("console.log(%q)", "hello world")
	return nil
}

func (p *syncPage) PickTheme(ctx *via.Ctx) error {
	ctx.PatchSignal("_picoTheme", "purple")
	return nil
}

func (p *syncPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.ID("root"), h.P(h.Text("ready")))
}

func TestSyncElements_pushesManualPatchOverSSE(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("PushList").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `id="results"`, "first")
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

func TestCtx_pushHelpersToleratesNilReceiver(t *testing.T) {
	t.Parallel()
	// Every push.go helper has `if ctx == nil { return }` as its first
	// line. A regression that dropped any one of those guards would
	// panic on a nil-pointer method call. None of these are realistic
	// user code, but the defensive guards are part of the contract.
	var ctx *via.Ctx
	cases := []struct {
		name string
		fn   func()
	}{
		{"ExecScript", func() { ctx.ExecScript("x") }},
		{"ExecScriptf", func() { ctx.ExecScriptf("x %d", 1) }},
		{"Reload", func() { ctx.Reload() }},
		{"Toast", func() { ctx.Toast("hi") }},
		{"Redirect", func() { ctx.Redirect("/") }},
		{"PatchSignals", func() { ctx.PatchSignals(map[string]any{"k": 1}) }},
		{"SyncElements", func() { ctx.SyncElements(h.Div()) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.NotPanics(t, c.fn)
		})
	}
}

func TestPatchSignals_emptyAndNilMapAreNoOps(t *testing.T) {
	t.Parallel()
	// Plural counterpart to PatchSignal's empty-key guard. The body has
	// `if len(values) == 0 { return }` which covers both nil maps and
	// zero-length maps. Pin both shapes so a refactor that drops the
	// guard doesn't start enqueueing empty signal frames.
	p := &syncPage{}
	ctx := viatest.NewCtx(t, p)

	ctx.PatchSignals(nil)
	assert.Empty(t, ctx.PendingSignals(),
		"PatchSignals(nil) must not enqueue anything")

	ctx.PatchSignals(map[string]any{})
	assert.Empty(t, ctx.PendingSignals(),
		"PatchSignals(empty map) must not enqueue anything")
}

func TestPatchSignal_emptyKeyIsNoOp(t *testing.T) {
	t.Parallel()
	// Empty key short-circuits before reaching the queue — documented
	// in PatchSignal's body. Pin it so a refactor that drops the guard
	// doesn't start enqueueing meaningless `"": value` entries that
	// json.Marshal would faithfully forward to the client.
	p := &syncPage{}
	ctx := viatest.NewCtx(t, p)
	ctx.PatchSignal("", "ignored")
	assert.Empty(t, ctx.PendingSignals(),
		"PatchSignal with empty key must not enqueue anything")
}

func TestPatchSignal_pushesKeyedValueToClient(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("PickTheme").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `"_picoTheme":"purple"`)
}

func TestExecScriptf_formatsArgsBeforeQueueing(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[syncPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Toast").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, `console.log("hello world")`)
}

// Stream — periodic ticker helper

type clockPage struct {
	Tick via.State[int]

	ticks atomic.Int32
}

func (p *clockPage) OnConnect(ctx *via.Ctx) error {
	via.Stream(ctx, 20*time.Millisecond, func(ctx *via.Ctx, t time.Time) {
		p.ticks.Add(1)
		p.Tick.Set(ctx, int(p.ticks.Load()))
	})
	return nil
}

func (p *clockPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.P(p.Tick.Text()))
}

func TestStream_pushesPeriodicUpdatesOverSSE(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[clockPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	// <p>3</p> proves the ticker fired at least 3x.
	viatest.AwaitFrame(t, frames, 2*time.Second, "<p>3</p>")
}

func TestStream_stopsWhenCtxDone(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[clockPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	_, cancel := tc.SSE()
	time.Sleep(120 * time.Millisecond)
	cancel()

	resp, err := server.Client().Post(server.URL+"/_sse/close", "text/plain", strings.NewReader(tc.TabID()))
	require.NoError(t, err)
	resp.Body.Close()

	// Race detector catches ticker leaks via the test runner.
	time.Sleep(80 * time.Millisecond)
}

// Ticker returned by Stream — pause/resume/set-interval surface

type tickerPage struct {
	ticks atomic.Int32
}

func (p *tickerPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestTicker_pauseStopsAndResumeRestartsCallback(t *testing.T) {
	t.Parallel()
	p := &tickerPage{}
	ctx := viatest.NewCtx(t, p)
	ticker := via.Stream(ctx, 10*time.Millisecond, func(ctx *via.Ctx, _ time.Time) {
		p.ticks.Add(1)
	})
	require.NotNil(t, ticker, "Stream should return a *via.Ticker handle")

	time.Sleep(40 * time.Millisecond)
	pre := p.ticks.Load()
	require.GreaterOrEqual(t, pre, int32(2),
		"ticker should fire at least twice in 40ms at 10ms interval")

	ticker.Pause()
	// Allow one in-flight callback to land, then snapshot.
	time.Sleep(15 * time.Millisecond)
	pausedAt := p.ticks.Load()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, pausedAt, p.ticks.Load(),
		"no further ticks should fire while paused")

	ticker.Resume()
	time.Sleep(40 * time.Millisecond)
	require.Greater(t, p.ticks.Load(), pausedAt,
		"ticks should resume after Resume")
}

func TestTicker_Paused_reflectsPauseAndResumeTransitions(t *testing.T) {
	t.Parallel()
	p := &tickerPage{}
	ctx := viatest.NewCtx(t, p)
	ticker := via.Stream(ctx, 200*time.Millisecond, func(*via.Ctx, time.Time) {})
	require.NotNil(t, ticker)

	assert.False(t, ticker.Paused(),
		"a freshly-started ticker must report Paused() == false")
	ticker.Pause()
	assert.True(t, ticker.Paused(),
		"Pause must flip Paused() to true")
	ticker.Resume()
	assert.False(t, ticker.Paused(),
		"Resume must flip Paused() back to false")
}

func TestTicker_setIntervalChangesCadence(t *testing.T) {
	t.Parallel()
	p := &tickerPage{}
	ctx := viatest.NewCtx(t, p)
	// Start slow so we can observe the cadence change.
	ticker := via.Stream(ctx, 200*time.Millisecond, func(ctx *via.Ctx, _ time.Time) {
		p.ticks.Add(1)
	})
	time.Sleep(60 * time.Millisecond)
	require.Zero(t, p.ticks.Load(),
		"no tick should have fired yet at the slow cadence")

	ticker.SetInterval(10 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.GreaterOrEqual(t, p.ticks.Load(), int32(2),
		"after SetInterval(10ms) the callback should fire several times in 50ms")
}
