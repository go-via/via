package vt_test

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder stands in for the test's own T so a vt failure can be read
// instead of failing the test that provokes it.
type recorder struct {
	testing.TB
	mu    sync.Mutex
	fatal string
	logs  []string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.fatal = fmt.Sprintf(format, args...)
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recorder) Fatal(args ...any) { r.Fatalf("%s", fmt.Sprint(args...)) }

func (r *recorder) Log(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprint(args...))
}

func (r *recorder) failure(fn func()) string {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatal
}

type tally struct {
	N    via.State[int]
	Tail string
}

func (p *tally) Inc(ctx *via.Ctx) { p.N.Set(p.N.Get() + 1) }

func (p *tally) View() h.H {
	return h.Div(h.Button(on.Click(p.Inc)), h.P(h.Str("n="), p.N.Display()), h.P(h.Str(p.Tail)))
}

func TestConnAwait_timeoutShowsTheFramesThatDidArrive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		app := vt.Serve(rec, via.Handler(tally{}, via.WithLogger(vt.Logger(t))))
		conn := app.Connect()
		for range 3 {
			status, _ := app.Action(0).Over(conn).Fire()
			require.Equal(t, http.StatusNoContent, status)
		}
		conn.Await("n=3")

		msg := rec.failure(func() { conn.Await("n=4") })
		assert.Contains(t, msg, `timed out after 2s waiting for "n=4"`)
		assert.Contains(t, msg, "4 frames on this stream, 0 read by this wait; the last 3:")
		assert.Contains(t, msg, "datastar-patch-elements: elements <div id=\"root\">")
		assert.Contains(t, msg, "n=3", "the newest frame must be among those shown")
		assert.NotContains(t, msg, "viatab", "only the last 3 frames are shown, not the first")
	})
}

func TestConnAwait_truncatesALongFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		app := vt.Serve(rec, via.Handler(tally{Tail: strings.Repeat("x", 5000) + "END"}, via.WithLogger(vt.Logger(t))))
		conn := app.Connect()
		status, _ := app.Action(0).Over(conn).Fire()
		require.Equal(t, http.StatusNoContent, status)

		msg := rec.failure(func() { conn.Await("never") })
		assert.Contains(t, msg, "bytes cut")
		assert.Contains(t, msg, "END</p>", "the frame's tail must be shown")
		assert.Less(t, len(msg), 2000, "a 5 KB frame must not be printed whole")
	})
}

func TestConnAwait_closedStreamShowsTheFramesThatDidArrive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		r := via.Handler(tally{}, via.WithLogger(vt.Logger(t)))
		app := vt.Serve(rec, r)
		conn := app.Connect()
		r.Close()

		msg := rec.failure(func() { conn.Await("n=1") })
		assert.Contains(t, msg, `stream closed before "n=1" arrived`)
		assert.Contains(t, msg, "viatab", "the frames seen before the close must be listed")
	})
}

func TestLogger_sendsViaDiagnosticsToTheTestLog(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	via.Handler(tally{}, via.WithLogger(vt.Logger(rec)))
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.logs, 1)
	assert.Contains(t, rec.logs[0], "level=WARN")
	assert.Contains(t, rec.logs[0], "WithTrustedOrigin")
}

// finished runs its cleanups on demand, so a test can log into a T whose test
// has ended, where the real t.Log panics.
type finished struct {
	recorder
	cleanups []func()
}

func (f *finished) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

func (f *finished) end() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func TestLogger_dropsARecordLoggedAfterTheTestEnded(t *testing.T) {
	t.Parallel()
	f := &finished{recorder: recorder{TB: t}}
	r := via.NewRouter(via.WithLogger(vt.Logger(f)))
	f.end()
	via.Mount(r, "/", misnamed{})
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.logs, 1, "only the startup warning, logged before the end")
	assert.NotContains(t, f.logs[0], "Oninit")
}

type misnamed struct{}

func (misnamed) View() h.H              { return h.Div() }
func (*misnamed) Oninit(*via.Ctx) error { return nil }
