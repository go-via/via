package via

import (
	"bytes"
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
// vs non-empty; Redirect short-circuits on empty input and Patch.Elements
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

// newCtx allocates a Ctx wired to the descriptor's slot bindings and
// scope keys. The production path layers app / session / writer /
// request on top of the returned ctx.
func newCtx(d *cmpDescriptor, cmpVal reflect.Value, id string) *Ctx {
	ctx := &Ctx{
		id:           id,
		desc:         d,
		signalRefs:   make([]signalRef, len(d.signalSlots)),
		dirtySignals: newBitset(len(d.signalSlots)),
		queue:        newPatchQueue(),
		doneChan:     make(chan struct{}),
	}
	ctx.ctxR = &CtxR{ctx: ctx}
	ctx.Patch = &Patch{ctx: ctx}
	ctx.touch()
	ctx.cmpReflect = cmpVal
	bindSlots(ctx, cmpVal, d)
	bindScopeKeys(cmpVal, d)
	bindFileKeys(cmpVal, d)
	bindDispatchFns(ctx, cmpVal, d)
	return ctx
}

// bindDispatchFns extracts each lifecycle/action method as a typed
// method value bound to *C, eliminating reflect.Value.Call on the
// per-request hot path. Called once per newCtx; the resulting funcs
// dispatch directly.
func bindDispatchFns(ctx *Ctx, cmpVal reflect.Value, d *cmpDescriptor) {
	ctx.viewFn = cmpVal.Method(d.viewIdx).Interface().(func(*CtxR) h.H)
	if d.initIdx >= 0 {
		ctx.initFn = cmpVal.Method(d.initIdx).Interface().(func(*Ctx) error)
	}
	if d.connectIdx >= 0 {
		ctx.connectFn = cmpVal.Method(d.connectIdx).Interface().(func(*Ctx) error)
	}
	if d.disposeIdx >= 0 {
		ctx.disposeFn = cmpVal.Method(d.disposeIdx).Interface().(func(*Ctx))
	}
	if n := len(d.actionSlots); n > 0 {
		ctx.actionFns = make([]func(*Ctx) error, n)
		for i, slot := range d.actionSlots {
			raw := cmpVal.Method(slot.methodIndex).Interface()
			if slot.voidReturn {
				fn := raw.(func(*Ctx))
				ctx.actionFns[i] = func(c *Ctx) error { fn(c); return nil }
			} else {
				ctx.actionFns[i] = raw.(func(*Ctx) error)
			}
		}
	}
}

// bindSlots writes the slot index and wire key into every Signal[T] / StateTab[T]
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

// bindScopeKeys writes the wire key into every StateSess[T] / StateApp[T]
// field of the freshly allocated *C by calling the handle's bindWireKey
// method. The scopeBinder interface assertion is checked once at Mount
// time, so the per-request path is a straight method call.
func bindScopeKeys(cmpVal reflect.Value, d *cmpDescriptor) {
	if len(d.scopeSlots) == 0 {
		return
	}
	elem := cmpVal.Elem()
	for _, s := range d.scopeSlots {
		field := fieldByPath(elem, s.fieldPath)
		field.Addr().Interface().(scopeBinder).bindWireKey(s.wireKey)
	}
}

// fieldByPath walks a chain of struct field indices, dereferencing pointer
// fields along the way.
func fieldByPath(v reflect.Value, path []int) reflect.Value {
	for _, idx := range path {
		v = v.Field(idx)
		if v.Kind() == reflect.Pointer {
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
		if c.connected.Load() > 0 {
			continue // a live SSE stream keeps the tab alive regardless of lastAccess
		}
		if c.lastAccess.Load() < cutoff {
			expired = append(expired, c)
			delete(a.contextRegistry, id)
		}
	}
	a.contextRegistryMu.Unlock()
	for _, c := range expired {
		a.disposeCtx(c, disconnectTTL)
	}
}

// signalDispose marks the ctx disposed and closes its Done channel so
// any SSE drain loop or Stream goroutine wakes and exits. Does not run
// OnDispose; idempotent — reports whether THIS call performed the
// disposal (false if an earlier caller already did). Used to break
// long-lived selects early during Shutdown. reason is recorded so the
// woken SSE loop can label via.sse.disconnect, and — for server-side
// reclamation (ttl / shutdown, not a client close) — counts via.ctx.reap
// once, at this idempotent chokepoint.
func (a *App) signalDispose(ctx *Ctx, reason string) bool {
	ctx.mu.Lock()
	if ctx.disposed {
		ctx.mu.Unlock()
		return false
	}
	ctx.disposed = true
	ctx.disposeReason = reason
	close(ctx.doneChan)
	ctx.mu.Unlock()
	if reason != disconnectClient {
		a.metricsOrNoop().Counter("via.ctx.reap", "reason", reason)
	}
	return true
}

// disposeCtx closes the ctx (idempotent with signalDispose) and runs
// OnDispose if defined. Serialized against in-flight actions via
// actionMu so OnDispose sees a composition that isn't being mutated by
// a concurrent handler. reason is threaded to signalDispose to label
// the via.sse.disconnect counter on the woken SSE loop.
func (a *App) disposeCtx(ctx *Ctx, reason string) {
	a.signalDispose(ctx, reason)

	ctx.actionMu.Lock()
	defer ctx.actionMu.Unlock()

	if ctx.disposeFn == nil {
		return
	}
	defer recoverLog(ctx, "OnDispose")
	// disposeFn may itself observe ctx.disposed; the flag was set in
	// signalDispose before actionMu was taken, so OnDispose sees a
	// consistent "yes, disposed" view.
	ctx.disposeFn(ctx)
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
