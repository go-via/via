package via_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctxRecordingBackplane wraps a real in-memory backplane and captures the
// context handed to its I/O methods, so a test can prove Shutdown cancels
// in-flight backplane work rather than leaving it to block forever.
type ctxRecordingBackplane struct {
	via.Backplane
	mu      sync.Mutex
	subCtx  context.Context
	snapCtx context.Context
	casCtx  context.Context
}

func (b *ctxRecordingBackplane) Subscribe(ctx context.Context, key string, from via.Offset) (<-chan via.Record, error) {
	b.mu.Lock()
	if b.subCtx == nil {
		b.subCtx = ctx
	}
	b.mu.Unlock()
	return b.Backplane.Subscribe(ctx, key, from)
}

func (b *ctxRecordingBackplane) LoadSnapshot(ctx context.Context, key string) ([]byte, via.Rev, bool, error) {
	b.mu.Lock()
	if b.snapCtx == nil {
		b.snapCtx = ctx
	}
	b.mu.Unlock()
	return b.Backplane.LoadSnapshot(ctx, key)
}

func (b *ctxRecordingBackplane) CAS(ctx context.Context, key string, expected via.Rev, data []byte) (via.Rev, error) {
	b.mu.Lock()
	if b.casCtx == nil {
		b.casCtx = ctx
	}
	b.mu.Unlock()
	return b.Backplane.CAS(ctx, key, expected, data)
}

func (b *ctxRecordingBackplane) captured() (sub, snap context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subCtx, b.snapCtx
}

func (b *ctxRecordingBackplane) capturedCAS() context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.casCtx
}

type bpCtxPage struct {
	Log via.StateAppEvents[tickEvent, int]
}

func (p *bpCtxPage) View(ctx *via.CtxR) h.H { return h.Div(p.Log.Text(ctx)) }

// A backend that hangs must not be able to wedge Shutdown: the framework hands
// every backplane call a context it cancels when the app shuts down. Without
// that, a stuck Subscribe/Append/CAS keeps its context.Background() alive and
// the pod can never drain. This is the most concrete production blocker the
// critic panel flagged.
func TestShutdownCancelsInflightBackplaneContext(t *testing.T) {
	t.Parallel()

	bp := &ctxRecordingBackplane{Backplane: via.InMemory()}
	app := via.New(via.WithBackplane(bp))
	server := vt.Serve(t, app)
	via.Mount[bpCtxPage](app, "/")
	_ = vt.NewClient(t, server, "/")

	var sub, snap context.Context
	require.Eventually(t, func() bool {
		sub, snap = bp.captured()
		return sub != nil && snap != nil
	}, 2*time.Second, 5*time.Millisecond,
		"the per-key projector must Subscribe and LoadSnapshot through the bound backplane")

	require.NoError(t, sub.Err(),
		"the backplane context must be live while the app is running")
	require.NoError(t, snap.Err(),
		"the backplane context must be live while the app is running")

	require.NoError(t, app.Shutdown(context.Background()))

	assert.ErrorIs(t, sub.Err(), context.Canceled,
		"Shutdown must cancel the context handed to in-flight EventLog.Subscribe")
	assert.ErrorIs(t, snap.Err(), context.Canceled,
		"Shutdown must cancel the context handed to in-flight Store.LoadSnapshot")
}

// blockingSubBackplane wraps a real in-memory backplane but hands out a
// Subscribe channel that never closes on its own — it only unblocks the reader
// when the call's context is cancelled. This models a backend that does NOT
// promptly close the channel on shutdown, the case a naive `for rec := range ch`
// projector would hang on. The runtime must (a) wake the loop on ctx.Done and
// (b) bound the Shutdown wait so a stuck goroutine can never wedge the drain.
type blockingSubBackplane struct {
	via.Backplane
}

func (b *blockingSubBackplane) Subscribe(ctx context.Context, _ string, _ via.Offset) (<-chan via.Record, error) {
	ch := make(chan via.Record)
	go func() {
		<-ctx.Done()
		// Deliberately do NOT close(ch): a `range ch` would block forever here.
	}()
	return ch, nil
}

// A backend whose Subscribe channel never closes must not be able to wedge
// Shutdown: the projector loop wakes on its context being cancelled and exits,
// and Shutdown's wait for background goroutines is bounded by the shutdown ctx
// regardless. Either property alone keeps the drain prompt; this asserts the
// observable contract — Shutdown returns well within its deadline.
func TestShutdownDoesNotHangOnNonClosingSubscribe(t *testing.T) {
	t.Parallel()

	bp := &blockingSubBackplane{Backplane: via.InMemory()}
	app := via.New(via.WithBackplane(bp))
	server := vt.Serve(t, app)
	via.Mount[bpCtxPage](app, "/")
	_ = vt.NewClient(t, server, "/") // spawns the per-key projector

	// Give the projector a moment to enter its Subscribe range loop.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = app.Shutdown(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown hung on a non-closing Subscribe channel; the bounded wait did not fire")
	}
	require.NoError(t, ctx.Err(),
		"Shutdown must return before its own deadline, proving the wait is prompt not merely bounded")
}

// subscribeErrBackplane fails every Subscribe so the projector's
// subscribe-failure branch fires. Other calls delegate to a real in-memory
// backplane so the page still mounts.
type subscribeErrBackplane struct {
	via.Backplane
}

func (b *subscribeErrBackplane) Subscribe(context.Context, string, via.Offset) (<-chan via.Record, error) {
	return nil, errors.New("boom: subscribe rejected")
}

// A dropped backplane error used to vanish silently, so an operator saw state
// divergence with no cause in the logs. The runtime must WARN-log a Subscribe
// failure (with a stable, greppable prefix) before the goroutine returns.
func TestProjectorLogsSubscribeFailure(t *testing.T) {
	t.Parallel()

	logger := &captureLogger{}
	bp := &subscribeErrBackplane{Backplane: via.InMemory()}
	app := via.New(via.WithBackplane(bp), via.WithLogger(logger), via.WithLogLevel(via.LogWarn))
	server := vt.Serve(t, app)
	via.Mount[bpCtxPage](app, "/")
	_ = vt.NewClient(t, server, "/") // spawns the projector → Subscribe fails

	require.Eventually(t, func() bool {
		for _, r := range logger.snapshot() {
			if r.level == via.LogWarn && strings.Contains(r.msg, "via: backplane subscribe failed") {
				return true
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond,
		"a swallowed Subscribe error must surface as a greppable WARN, not vanish")

	require.NoError(t, app.Shutdown(context.Background()))
}

type bpWritePage struct {
	N via.StateAppNum[int]
}

func (p *bpWritePage) Inc(ctx *via.Ctx)       { p.N.Op(ctx).Inc() }
func (p *bpWritePage) View(ctx *via.CtxR) h.H { return h.Div(on.Click(p.Inc), p.N.Text(ctx)) }

// The write path (StateApp.Update's read-modify-write CAS) must ride the same
// shutdown-cancelled context as reads — otherwise a wedged backend's CAS keeps
// an action goroutine alive and blocks the drain. This guards the RMW call
// sites that a read-only fix would miss.
func TestShutdownCancelsBackplaneWritePathContext(t *testing.T) {
	t.Parallel()

	bp := &ctxRecordingBackplane{Backplane: via.InMemory()}
	app := via.New(via.WithBackplane(bp))
	server := vt.Serve(t, app)
	via.Mount[bpWritePage](app, "/")

	c := vt.NewClient(t, server, "/")
	require.Equal(t, 200, c.Action("Inc").Fire())

	var cas context.Context
	require.Eventually(t, func() bool {
		cas = bp.capturedCAS()
		return cas != nil
	}, 2*time.Second, 5*time.Millisecond,
		"StateApp.Update must CAS through the bound backplane")

	require.NoError(t, cas.Err(),
		"the backplane write context must be live while the app is running")

	require.NoError(t, app.Shutdown(context.Background()))

	assert.ErrorIs(t, cas.Err(), context.Canceled,
		"Shutdown must cancel the context handed to in-flight Store.CAS")
}
