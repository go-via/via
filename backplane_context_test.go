package via_test

import (
	"context"
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
