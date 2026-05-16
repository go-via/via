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

func TestTicker_PauseStopsAndResumeRestartsCallback(t *testing.T) {
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

func TestTicker_SetIntervalChangesCadence(t *testing.T) {
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
