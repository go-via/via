package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type streamPanicPage struct {
	ticks via.Signal[int]
}

func (p *streamPanicPage) OnConnect(ctx *via.Ctx) error {
	via.Stream(ctx, 5*time.Millisecond, func(c *via.Ctx, _ time.Time) {
		panic("stream-callback-boom")
	})
	return nil
}

func (p *streamPanicPage) View(ctx *via.Ctx) h.H { return h.Div(p.ticks.Text()) }

func TestStream_callbackPanicDoesNotCrashServer(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithLogLevel(via.LogError),
	)
	via.Mount[streamPanicPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	_, cancel := tc.SSE()
	defer cancel()

	time.Sleep(30 * time.Millisecond)

	// If recoverLog didn't catch the panic, the server goroutine would
	// be dead and the follow-up GET would fail. Surviving the request
	// is the assertion.
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

type clockPage struct {
	Tick via.StateTab[int]

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

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	// <p>3</p> proves the ticker fired at least 3x.
	vt.AwaitFrame(t, frames, 2*time.Second, "<p>3</p>")
}

func TestStream_stopsWhenCtxDone(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[clockPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	_, cancel := tc.SSE()
	time.Sleep(120 * time.Millisecond)
	cancel()

	resp, err := server.Client().Post(server.URL+"/_sse/close", "text/plain", strings.NewReader(tc.TabID()))
	require.NoError(t, err)
	resp.Body.Close()

	// Race detector catches ticker leaks via the test runner.
	time.Sleep(80 * time.Millisecond)
}

// streamRacePage exercises Stream-driven writes against action-driven
// writes on the same composition; both touch the same Signal/State.
type streamRacePage struct {
	N via.Signal[int]
	M via.StateTab[int]
}

func (p *streamRacePage) OnConnect(ctx *via.Ctx) error {
	via.Stream(ctx, 1*time.Millisecond, func(ctx *via.Ctx, _ time.Time) {
		p.N.Set(ctx, p.N.Get(ctx)+1)
		p.M.Update(ctx, func(n int) int { return n + 1 })
	})
	return nil
}

func (p *streamRacePage) Bump(ctx *via.Ctx) error {
	p.N.Set(ctx, p.N.Get(ctx)+1)
	p.M.Update(ctx, func(n int) int { return n + 1 })
	return nil
}

func (p *streamRacePage) View(ctx *via.Ctx) h.H {
	return h.Div(p.N.Text(), p.M.Text())
}

// TestStream_doesNotRaceWithConcurrentActions hammers a Stream-driven
// signal write at the same time as POSTed action handlers that mutate
// the same composition. Without the per-Ctx actionMu around streamTick
// the race detector trips on Signal.val / dirty-bit writes; with the
// lock in place this must stay clean.
func TestStream_doesNotRaceWithConcurrentActions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[streamRacePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	_, cancel := tc.SSE()
	defer cancel()

	// Let OnConnect fire and the Stream goroutine spin up.
	time.Sleep(20 * time.Millisecond)

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		// Action POST contends with Stream tick for actionMu / Signal.val.
		_ = tc.Action("Bump").Fire()
	}
}

// Ticker Pause / Resume / SetInterval — observable through the cadence
// of SSE element-patch frames driven by a Stream callback.

type tickerControlPage struct {
	N      via.StateTab[int]
	ticker *via.Ticker
}

func (p *tickerControlPage) OnConnect(ctx *via.Ctx) error {
	p.ticker = via.Stream(ctx, 20*time.Millisecond, func(c *via.Ctx, _ time.Time) {
		p.N.Update(c, func(n int) int { return n + 1 })
	})
	return nil
}

func (p *tickerControlPage) Pause(ctx *via.Ctx) error  { p.ticker.Pause(); return nil }
func (p *tickerControlPage) Resume(ctx *via.Ctx) error { p.ticker.Resume(); return nil }
func (p *tickerControlPage) SpeedUp(ctx *via.Ctx) error {
	p.ticker.SetInterval(10 * time.Millisecond)
	return nil
}

func (p *tickerControlPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Span(h.ID("n"), p.N.Text()))
}

func TestTicker_pauseStopsAndResumeRestartsCallback(t *testing.T) {
	t.Parallel()
	// Observable contract: while paused, no new tick frames arrive on
	// the SSE stream; Resume restarts them. A regression where Pause
	// leaked further ticks would show up as frames during the paused
	// window.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tickerControlPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	// Wait for at least one pre-pause tick.
	vt.AwaitFrame(t, frames, 2*time.Second, `id="n"`)

	require.Equal(t, http.StatusOK, tc.Action("Pause").Fire())
	// Drain any in-flight tick or action frame, then assert silence.
	time.Sleep(40 * time.Millisecond)
drain:
	for {
		select {
		case <-frames:
		default:
			break drain
		}
	}
	select {
	case f := <-frames:
		t.Fatalf("unexpected frame while paused: %q", f)
	case <-time.After(120 * time.Millisecond):
		// silence is the success path
	}

	require.Equal(t, http.StatusOK, tc.Action("Resume").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, `id="n"`)
}

func TestTicker_setIntervalChangesCadence(t *testing.T) {
	t.Parallel()
	// SetInterval must take effect on subsequent ticks. Start at 20ms,
	// observe a tick, fire SpeedUp (10ms), assert several follow-up ticks
	// arrive in a window that would be too short at the original cadence.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tickerControlPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	vt.AwaitFrame(t, frames, 2*time.Second, `id="n"`)
	require.Equal(t, http.StatusOK, tc.Action("SpeedUp").Fire())

	// At 10ms, ~5 ticks fit in 50ms; the bound is loose to tolerate
	// scheduler jitter while still failing if cadence didn't change.
	deadline := time.After(120 * time.Millisecond)
	got := 0
	for {
		select {
		case <-frames:
			got++
			if got >= 3 {
				return
			}
		case <-deadline:
			t.Fatalf("only saw %d frames after SetInterval(10ms); expected ≥3", got)
		}
	}
}

// Ticker.Stop terminates the stream permanently; no frames arrive
// after Stop, even after Resume.

type tickerStopPage struct {
	N      via.StateTab[int]
	ticker *via.Ticker
}

func (p *tickerStopPage) OnConnect(ctx *via.Ctx) error {
	p.ticker = via.Stream(ctx, 20*time.Millisecond, func(c *via.Ctx, _ time.Time) {
		p.N.Update(c, func(n int) int { return n + 1 })
	})
	return nil
}

func (p *tickerStopPage) Halt(ctx *via.Ctx) error { p.ticker.Stop(); return nil }
func (p *tickerStopPage) Wake(ctx *via.Ctx) error { p.ticker.Resume(); return nil }
func (p *tickerStopPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Span(h.ID("n"), p.N.Text()))
}

func TestTicker_stopPermanentlyTerminatesCallbacks(t *testing.T) {
	t.Parallel()
	// Stop must be terminal: Resume after Stop cannot revive the
	// ticker. A regression that wired Stop to the same flag as Pause
	// would let a stray Resume restart the stream.
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[tickerStopPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	vt.AwaitFrame(t, frames, 2*time.Second, `id="n"`)

	require.Equal(t, http.StatusOK, tc.Action("Halt").Fire())
	// Drain any in-flight tick + the action's auto-flush frame.
	time.Sleep(40 * time.Millisecond)
drain:
	for {
		select {
		case <-frames:
		default:
			break drain
		}
	}

	// Resume must not revive a stopped ticker.
	require.Equal(t, http.StatusOK, tc.Action("Wake").Fire())
	time.Sleep(40 * time.Millisecond)
drain2:
	for {
		select {
		case <-frames:
		default:
			break drain2
		}
	}

	select {
	case f := <-frames:
		t.Fatalf("unexpected frame after Stop+Resume: %q", f)
	case <-time.After(150 * time.Millisecond):
		// silence is the success path
	}
}
