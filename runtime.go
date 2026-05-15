package via

import (
	"bytes"
	"maps"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-via/via/h"
)

// tabSignalKey is the wire-protocol signal name carrying a Ctx's tab id.
// Every datastar payload (action POST, SSE handshake) must carry it; it
// doubles as the CSRF token (see memory: via_tab IS the CSRF token).
const tabSignalKey = "via_tab"

// renderBufPool reduces alloc churn on the patch render path. Buffers
// start at 8 KiB and grow as needed; we keep them around for the next
// render.
var renderBufPool = sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 8192)) },
}

func getRenderBuf() *bytes.Buffer {
	b := renderBufPool.Get().(*bytes.Buffer)
	b.Reset()
	return b
}

func putRenderBuf(b *bytes.Buffer) {
	if b.Cap() > 1<<20 { // drop >1 MiB outliers
		return
	}
	renderBufPool.Put(b)
}

// patchQueue coalesces outgoing patches between SSE flushes. The
// presence flags for elements and redirect are encoded as empty-string
// vs non-empty; Redirect short-circuits on empty input and SyncElements
// / flushDirty only set elements after rendering non-empty content, so
// the implication holds in both directions.
type patchQueue struct {
	mu       sync.Mutex
	elements string
	signals  map[string]any
	scripts  strings.Builder
	redirect string
	wake     chan struct{}
}

func newPatchQueue() *patchQueue {
	return &patchQueue{wake: make(chan struct{}, 1)}
}

func (q *patchQueue) notify() {
	if q == nil {
		return
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// NewBoundCtx returns a *Ctx bound to c with all Signal[T]/State[T]/
// scope fields wired up, ready for direct unit testing of action
// methods. No HTTP server, no session, no SSE — just a typed Ctx
// against a typed *C. The descriptor is the same one Mount[T] would
// build, but the resulting Ctx is detached from any App.
//
// Reserved for tests; the via/test package wraps it in
// test.NewCtx[T any](t, *T) so production code never imports it.
func NewBoundCtx[T any](c *T) *Ctx {
	return newCtx(buildDescriptor[T](), reflect.ValueOf(c), genTabID("test"))
}

// newCtx allocates a Ctx wired to the descriptor's slot bindings and
// scope keys. Shared between the production page-render path and
// NewBoundCtx for tests; the production path layers app / session /
// writer / request on top of the returned ctx.
func newCtx(d *cmpDescriptor, cmpVal reflect.Value, id string) *Ctx {
	ctx := &Ctx{
		id:           id,
		desc:         d,
		signalRefs:   make([]signalRef, len(d.signalSlots)),
		dirtySignals: newBitset(len(d.signalSlots)),
		queue:        newPatchQueue(),
		doneChan:     make(chan struct{}),
	}
	ctx.touch()
	ctx.cmpReflect = cmpVal
	bindSlots(ctx, cmpVal, d)
	bindScopeKeys(cmpVal, d)
	bindFileKeys(cmpVal, d)
	ctx.reflectArgs[0] = reflect.ValueOf(ctx)
	return ctx
}

// PatchSignal queues a single signal update keyed by name. Plugins use it
// to push values to client-only signals they own (e.g. picocss's
// "_picoTheme") without going through a typed Signal[T] handle. Multiple
// PatchSignal calls within the same flush window are merged — last write
// wins per key.
func (ctx *Ctx) PatchSignal(key string, value any) {
	if key == "" {
		return
	}
	ctx.PatchSignals(map[string]any{key: value})
}

// PatchSignals queues many signal updates as a single batched merge. Same
// last-wins-per-key semantics as PatchSignal.
func (ctx *Ctx) PatchSignals(values map[string]any) {
	if ctx == nil || ctx.queue == nil || len(values) == 0 {
		return
	}
	ctx.queue.mu.Lock()
	if ctx.queue.signals == nil {
		ctx.queue.signals = make(map[string]any, len(values))
	}
	maps.Copy(ctx.queue.signals, values)
	ctx.queue.mu.Unlock()
	ctx.queue.notify()
}

// SyncElements pushes one or more h.H trees to the client as element
// patches at the next flush. Useful for action-driven, targeted DOM
// updates that bypass the full view re-render. Each element should carry
// h.ID("...") so the client knows where to morph it.
func (ctx *Ctx) SyncElements(elements ...h.H) {
	if ctx == nil || ctx.queue == nil || len(elements) == 0 {
		return
	}
	buf := getRenderBuf()
	defer putRenderBuf(buf)
	for _, el := range elements {
		if el == nil {
			continue
		}
		_ = el.Render(buf)
	}
	if buf.Len() == 0 {
		return
	}
	ctx.queue.mu.Lock()
	ctx.queue.elements = buf.String()
	ctx.queue.mu.Unlock()
	ctx.queue.notify()
}

// bindSlots writes the slot index and wire key into every Signal[T] / State[T]
// field of the freshly allocated *C (including nested children), stashes a
// typed signalRef pointer for reflection-free dispatch, and applies the
// init=… tag value if any. Combined into one pass so we walk
// d.signalSlots only once per Ctx setup.
func bindSlots(ctx *Ctx, cmpVal reflect.Value, d *cmpDescriptor) {
	elem := cmpVal.Elem()
	for i, s := range d.signalSlots {
		field := fieldByPath(elem, s.fieldPath)
		ref := field.Addr().Interface().(signalRef)
		ref.bindSlot(uint16(i), s.wireKey)
		ctx.signalRefs[i] = ref
		if s.initRaw != "" {
			ref.decodeRaw(s.initRaw)
		}
	}
}

// bindScopeKeys writes the wire key into every scope.User[T] / scope.App[T]
// field of the freshly allocated *C by calling the handle's BindWireKey
// method. The scopeBinder interface assertion is checked once at Mount
// time, so the per-request path is a straight method call.
func bindScopeKeys(cmpVal reflect.Value, d *cmpDescriptor) {
	if len(d.scopeSlots) == 0 {
		return
	}
	elem := cmpVal.Elem()
	for _, s := range d.scopeSlots {
		field := fieldByPath(elem, s.fieldPath)
		field.Addr().Interface().(scopeBinder).BindWireKey(s.wireKey)
	}
}

// fieldByPath walks a chain of struct field indices, dereferencing pointer
// fields along the way.
func fieldByPath(v reflect.Value, path []int) reflect.Value {
	for _, idx := range path {
		v = v.Field(idx)
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
	}
	return v
}

// genTabID returns a route-prefixed random id for human-readable tab traces.
func genTabID(route string) string {
	return route + "_" + genSecureID()
}

// errFromCallOut extracts the error from a reflect.Method.Call return
// slice, handling the typed-nil and zero-output cases. Centralised so
// the three reflective dispatch sites (OnInit, OnConnect, action) read
// identically.
func errFromCallOut(out []reflect.Value) error {
	if len(out) == 0 || out[0].IsNil() {
		return nil
	}
	err, _ := out[0].Interface().(error)
	return err
}

func enqueueScript(ctx *Ctx, s string) {
	q := ctx.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	q.scripts.WriteString("try{")
	q.scripts.WriteString(s)
	q.scripts.WriteString("}catch(e){console.error(e)};")
	q.notify()
}

// runSweep drives a sweep goroutine: it ticks at interval and calls sweep
// on every tick, exiting when stopSweep closes. Used by both the session
// and context expirers — the only thing that varies is the cadence and
// the per-tick action. interval ≤ 0 falls back to the supplied default.
func (a *App) runSweep(interval, fallback time.Duration, sweep func()) {
	if interval <= 0 {
		interval = fallback
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopSweep:
			return
		case <-ticker.C:
			sweep()
		}
	}
}

func (a *App) removeExpiredContexts() {
	cutoff := time.Now().Add(-a.cfg.contextTTL).UnixNano()
	a.contextRegistryMu.Lock()
	var expired []*Ctx
	for id, c := range a.contextRegistry {
		if c.lastAccess.Load() < cutoff {
			expired = append(expired, c)
			delete(a.contextRegistry, id)
		}
	}
	a.contextRegistryMu.Unlock()
	for _, c := range expired {
		a.disposeCtx(c)
	}
}

// disposeCtx closes the ctx and runs OnDispose if defined.
func (a *App) disposeCtx(ctx *Ctx) {
	ctx.mu.Lock()
	if ctx.disposed {
		ctx.mu.Unlock()
		return
	}
	ctx.disposed = true
	close(ctx.doneChan)
	ctx.mu.Unlock()

	if ctx.desc != nil && ctx.desc.disposeIdx >= 0 && ctx.cmpReflect.IsValid() {
		defer recoverLog(ctx, "OnDispose")
		ctx.cmpReflect.Method(ctx.desc.disposeIdx).Call(ctx.reflectArgs[:])
	}
}

// recoverLog is a deferred-recover helper that logs the panic value via
// the App's logger. Use it as `defer recoverLog(ctx, "OnConnect")` from
// any callsite that wants to log-and-swallow a callback panic. recover()
// only works directly in a deferred func, so this helper IS the deferred
// func — it cannot be wrapped in another helper that calls it.
func recoverLog(ctx *Ctx, what string) {
	if rec := recover(); rec != nil && ctx != nil && ctx.app != nil {
		ctx.app.logErr(ctx, "%s panicked: %v", what, rec)
	}
}
