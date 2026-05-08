package via

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-via/via/h"
	"github.com/starfederation/datastar-go/datastar"
)

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

// patchQueue coalesces outgoing patches between SSE flushes.
type patchQueue struct {
	mu       sync.Mutex
	elements string
	hasElems bool
	signals  map[string]any
	scripts  strings.Builder
	redirect string
	hasRedir bool
	disposed bool
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
	d := buildDescriptor[T](nil, "")
	cmpVal := reflect.ValueOf(c)
	ctx := &Ctx{
		id:           "test_" + genSecureID(),
		desc:         d,
		cmpVal:       c,
		signalRefs:   make([]signalRef, len(d.signalSlots)),
		dirtySignals: newBitset(len(d.signalSlots)),
		queue:        newPatchQueue(),
		doneChan:     make(chan struct{}),
	}
	ctx.touch()
	bindSlots(ctx, cmpVal, d)
	bindScopeKeys(cmpVal, d)
	for i, s := range d.signalSlots {
		if s.initRaw != "" {
			ctx.signalRefs[i].decodeRaw(s.initRaw)
		}
	}
	ctx.reflectArgs[0] = reflect.ValueOf(ctx)
	return ctx
}

// PatchSignal queues a single signal update keyed by name. Plugins use it
// to push values to client-only signals they own (e.g. picocss's
// "_picoTheme") without going through a typed Signal[T] handle. Multiple
// PatchSignal calls within the same flush window are merged — last write
// wins per key.
func (ctx *Ctx) PatchSignal(key string, value any) {
	if ctx == nil || ctx.queue == nil || key == "" {
		return
	}
	ctx.queue.mu.Lock()
	if ctx.queue.signals == nil {
		ctx.queue.signals = make(map[string]any, 1)
	}
	ctx.queue.signals[key] = value
	ctx.queue.mu.Unlock()
	ctx.queue.notify()
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
	for k, v := range values {
		ctx.queue.signals[k] = v
	}
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
	ctx.queue.hasElems = true
	ctx.queue.mu.Unlock()
	ctx.queue.notify()
}

// renderPage handles GET on a Mount-ed route. Allocates a fresh *C, decodes
// path params + initial signal values, optionally calls Init, renders the
// view inside the HTML5 envelope.
func (a *App) renderPage(d *cmpDescriptor, w http.ResponseWriter, r *http.Request) {
	cmpVal := reflect.New(d.typ)
	cmpAny := cmpVal.Interface()

	tabID := genTabID(d.route)
	ctx := &Ctx{
		id:           tabID,
		app:          a,
		desc:         d,
		cmpVal:       cmpAny,
		signalRefs:   make([]signalRef, len(d.signalSlots)),
		dirtySignals: newBitset(len(d.signalSlots)),
		queue:        newPatchQueue(),
		doneChan:     make(chan struct{}),
		session:      a.sessionFromRequest(r),
		w:            w,
		r:            r,
	}
	ctx.touch()

	bindSlots(ctx, cmpVal, d)
	bindScopeKeys(cmpVal, d)
	applyInits(ctx, cmpVal, d)
	decodePathParams(cmpVal, r, d)
	decodeQueryParams(cmpVal, r, d)

	ctx.reflectArgs[0] = reflect.ValueOf(ctx)
	ctxArg := ctx.reflectArgs[:]

	if d.hasInit {
		out := cmpVal.Method(d.initIdx).Call(ctxArg)
		if !out[0].IsNil() {
			if errVal, ok := out[0].Interface().(error); ok && errVal != nil {
				a.logErr(ctx, "Init: %v", errVal)
			}
		}
	}

	a.registerCtx(tabID, ctx)

	view := cmpVal.Method(d.viewIdx).Call(ctxArg)[0]
	body := view.Interface().(h.H)

	a.writePageDocument(w, ctx, body)
}

func (a *App) writePageDocument(w http.ResponseWriter, ctx *Ctx, body h.H) {
	initialSigs := map[string]any{"via_tab": ctx.id}
	a.appSignalsMu.RLock()
	for k, v := range a.appSignals {
		initialSigs[k] = v
	}
	a.appSignalsMu.RUnlock()
	for slot, ref := range ctx.signalRefs {
		s := ctx.desc.signalSlots[slot]
		if s.kind != kindSignal {
			continue
		}
		v, err := ref.encode()
		if err != nil {
			continue
		}
		var raw json.RawMessage = v
		initialSigs[s.wireKey] = raw
	}

	sigsJSON, _ := json.Marshal(initialSigs)
	head := []h.H{
		h.Meta(h.Data("signals", string(sigsJSON))),
		h.Meta(h.Data("init", "@get('/_sse')")),
		h.Meta(h.Data("init", fmt.Sprintf(
			`window.addEventListener('beforeunload',(e)=>{navigator.sendBeacon('/_sse/close','%s');});`, ctx.id))),
	}
	head = append(head, a.documentHeadIncludes...)

	bodyEls := []h.H{h.Div(h.ID(ctx.id), body)}
	bodyEls = append(bodyEls, a.documentFootIncludes...)

	doc := h.HTML5(h.HTML5Props{
		Title:     a.cfg.title,
		Head:      head,
		Body:      bodyEls,
		HTMLAttrs: a.documentHTMLAttrs,
	})
	_ = doc.Render(w)
}

// bindSlots writes the slot index and wire key into every Signal[T] / State[T]
// field of the freshly allocated *C (including nested children), and stashes
// a typed signalRef pointer for reflection-free dispatch later.
func bindSlots(ctx *Ctx, cmpVal reflect.Value, d *cmpDescriptor) {
	elem := cmpVal.Elem()
	for i, s := range d.signalSlots {
		field := fieldByPath(elem, s.fieldPath)
		ref := field.Addr().Interface().(signalRef)
		ref.bindSlot(uint16(i), s.wireKey)
		ctx.signalRefs[i] = ref
	}
}

// bindScopeKeys writes the wire key into every scope.User[T] / scope.App[T]
// field of the freshly allocated *C. The handles' WireKey field is exported
// for this purpose.
func bindScopeKeys(cmpVal reflect.Value, d *cmpDescriptor) {
	if len(d.scopeSlots) == 0 {
		return
	}
	elem := cmpVal.Elem()
	for _, s := range d.scopeSlots {
		field := fieldByPath(elem, s.fieldPath)
		// scope.User / scope.App both expose WireKey as the first field.
		field.FieldByName("WireKey").SetString(s.wireKey)
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

// applyInits decodes init=… tag tokens into the typed value field. The
// value lives in an unexported field of the handle, so we go through the
// typed signalRef decoder rather than reflect.Value.SetX.
func applyInits(ctx *Ctx, cmpVal reflect.Value, d *cmpDescriptor) {
	for i, s := range d.signalSlots {
		if s.initRaw == "" {
			continue
		}
		ctx.signalRefs[i].decodeRaw(s.initRaw)
	}
	_ = cmpVal
}

func decodePathParams(cmpVal reflect.Value, r *http.Request, d *cmpDescriptor) {
	if len(d.paramSlots) == 0 {
		return
	}
	elem := cmpVal.Elem()
	for _, p := range d.paramSlots {
		raw := r.PathValue(p.name)
		decodeParam(fieldByPath(elem, p.fieldPath), p.kind, raw)
	}
}

func decodeQueryParams(cmpVal reflect.Value, r *http.Request, d *cmpDescriptor) {
	if len(d.querySlots) == 0 {
		return
	}
	q := r.URL.Query()
	elem := cmpVal.Elem()
	for _, p := range d.querySlots {
		raw := q.Get(p.name)
		if raw == "" {
			continue
		}
		decodeParam(fieldByPath(elem, p.fieldPath), p.kind, raw)
	}
}

// genTabID returns a route-prefixed random id for human-readable tab traces.
func genTabID(route string) string {
	return route + "_" + genSecureID()
}

// handleSSE opens the persistent stream for a Ctx identified by the via_tab
// signal sent in the URL, drains the patch queue until the client goes away
// or the ctx is disposed.
func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)
	tabID, _ := sigs["via_tab"].(string)

	ctx, err := a.getCtx(tabID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if ctx.session != nil && a.sessionFromRequest(r) != ctx.session {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	ctx.touch()

	// Same posture as the page render and action POST: run the
	// descriptor's group middleware so a requireAuth-style guard can
	// veto the SSE handshake before the stream goes hot.
	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runSSEStream(a, ctx, w, r)
	})
	applyMiddleware(ctx.desc.groupMW, stream).ServeHTTP(w, r)
}

func runSSEStream(a *App, ctx *Ctx, w http.ResponseWriter, r *http.Request) {
	// OnConnect runs once, the first time the SSE stream is opened. Bots
	// that hit GET without ever opening the SSE never see this fire, so
	// expensive background work (tickers, fan-out goroutines) lives here
	// rather than in Init.
	ctx.connectOnce.Do(func() {
		if !ctx.desc.hasOnConnect {
			return
		}
		defer func() {
			if rec := recover(); rec != nil {
				a.logErr(ctx, "OnConnect panicked: %v", rec)
			}
		}()
		out := reflect.ValueOf(ctx.cmpVal).Method(ctx.desc.connectIdx).
			Call(ctx.reflectArgs[:])
		if !out[0].IsNil() {
			if errVal, _ := out[0].Interface().(error); errVal != nil {
				a.logErr(ctx, "OnConnect: %v", errVal)
			}
		}
	})

	sse := datastar.NewSSE(w, r)

	// Force-drain anything queued while the previous SSE was
	// disconnected — patches accumulated during the gap have no wake
	// notification waiting (it was either consumed by the dead loop or
	// never sent if the previous drain was mid-flight). Without this,
	// the reconnected client sees stale UI until the next notify.
	if hasPending(ctx.queue) {
		if err := drainQueue(sse, ctx); err != nil {
			return
		}
	}

	var heartbeat <-chan time.Time
	if a.cfg.sseHeartbeat > 0 {
		t := time.NewTicker(a.cfg.sseHeartbeat)
		defer t.Stop()
		heartbeat = t.C
	}

	for {
		select {
		case <-sse.Context().Done():
			return
		case <-ctx.doneChan:
			return
		case <-heartbeat:
			if err := sse.PatchSignals([]byte("{}")); err != nil {
				return
			}
			ctx.touch()
		case <-ctx.queue.wake:
			if err := drainQueue(sse, ctx); err != nil {
				return
			}
			ctx.touch()
		}
	}
}

// hasPending reports whether the patch queue holds anything to flush.
// Cheap snapshot under the lock — used by the SSE handshake to drain
// a backlog from the previous (dropped) connection without waiting for
// the next notify.
func hasPending(q *patchQueue) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.hasElems || q.hasRedir ||
		len(q.signals) > 0 || q.scripts.Len() > 0
}

func drainQueue(sse *datastar.ServerSentEventGenerator, ctx *Ctx) error {
	q := ctx.queue
	q.mu.Lock()
	elems, hasElems := q.elements, q.hasElems
	signals := q.signals
	scripts := q.scripts.String()
	redirect, hasRedir := q.redirect, q.hasRedir
	q.elements = ""
	q.hasElems = false
	q.signals = nil
	q.scripts.Reset()
	q.redirect = ""
	q.hasRedir = false
	q.mu.Unlock()

	if hasRedir {
		return sse.Redirect(redirect)
	}
	if hasElems {
		if err := sse.PatchElements(elems); err != nil {
			return err
		}
	}
	if len(signals) > 0 {
		out, _ := json.Marshal(signals)
		if err := sse.PatchSignals(out); err != nil {
			return err
		}
	}
	if scripts != "" {
		if err := sse.ExecuteScript(scripts); err != nil {
			return err
		}
	}
	return nil
}

// handleAction dispatches POST /_action/{cmpID}.{methodName} (or just
// /_action/{methodName} for root). The {id} URL segment encodes both.
func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	maxBody := a.cfg.maxRequestBody
	if maxBody == 0 {
		maxBody = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	var sigs map[string]any
	if err := datastar.ReadSignals(r, &sigs); err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
	}
	tabID, _ := sigs["via_tab"].(string)

	ctx, err := a.getCtx(tabID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ctx.session != nil && a.sessionFromRequest(r) != ctx.session {
		http.Error(w, "session mismatch", http.StatusForbidden)
		return
	}

	d := ctx.desc
	slotIdx, ok := d.actionByName[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	slot := d.actionSlots[slotIdx]

	// Wrap the dispatch in the descriptor's group middleware so a
	// requireAuth (or any group-level guard) checks the request before
	// the action runs — same auth posture as the rendered route.
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runAction(a, ctx, slot, id, w, r, sigs)
	})
	applyMiddleware(d.groupMW, dispatch).ServeHTTP(w, r)
}

func runAction(a *App, ctx *Ctx, slot actionSlot, id string,
	w http.ResponseWriter, r *http.Request, sigs map[string]any) {
	// Serialize per-tab so parallel POSTs to the same ctx don't race
	// on State writes, dirty bits, or Writer/Request assignment.
	ctx.actionMu.Lock()
	defer ctx.actionMu.Unlock()

	ctx.mu.Lock()
	ctx.w = w
	ctx.r = r
	ctx.mu.Unlock()
	defer func() {
		ctx.mu.Lock()
		ctx.w = nil
		ctx.r = nil
		ctx.mu.Unlock()
	}()
	defer func() {
		if rec := recover(); rec != nil {
			a.logErr(ctx, "action %q panicked: %v", id, rec)
			panicErr := fmt.Errorf("panic: %v", rec)
			a.dispatchActionError(ctx, panicErr, true)
		}
	}()

	ctx.lastSignals = sigs
	injectSignals(ctx, sigs)

	cmpVal := reflect.ValueOf(ctx.cmpVal)
	out := cmpVal.Method(slot.methodIndex).Call(ctx.reflectArgs[:])
	if !out[0].IsNil() {
		errVal, _ := out[0].Interface().(error)
		if errVal != nil {
			a.dispatchActionError(ctx, errVal, false)
		}
	}

	flushDirty(ctx)
}

func (a *App) dispatchActionError(ctx *Ctx, err error, fromPanic bool) {
	if a.cfg.actionErrorHandler != nil {
		a.cfg.actionErrorHandler(ctx, err)
		return
	}
	msg := err.Error()
	if fromPanic {
		msg = "Something went wrong"
	}
	out, _ := json.Marshal(msg)
	enqueueScript(ctx, "alert("+string(out)+")")
}

func enqueueScript(ctx *Ctx, s string) {
	if ctx == nil || ctx.queue == nil {
		return
	}
	q := ctx.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	q.scripts.WriteString("try{")
	q.scripts.WriteString(s)
	q.scripts.WriteString("}catch(e){console.error(e)};")
	q.notify()
}

// flushDirty re-renders the view fragment if any State changed and patches
// any dirty signals to the browser.
func flushDirty(ctx *Ctx) {
	if !ctx.stateDirty && !ctx.dirtySignals.any() {
		return
	}

	if ctx.stateDirty {
		buf := getRenderBuf()
		view := reflect.ValueOf(ctx.cmpVal).Method(ctx.desc.viewIdx).
			Call(ctx.reflectArgs[:])[0]
		body := view.Interface().(h.H)
		_ = h.Div(h.ID(ctx.id), body).Render(buf)
		ctx.queue.mu.Lock()
		ctx.queue.elements = buf.String()
		ctx.queue.hasElems = true
		ctx.queue.mu.Unlock()
		putRenderBuf(buf)
		ctx.stateDirty = false
	}

	if ctx.dirtySignals.any() {
		out := map[string]any{}
		for slot, ref := range ctx.signalRefs {
			if !ctx.dirtySignals.get(slot) {
				continue
			}
			s := ctx.desc.signalSlots[slot]
			b, err := ref.encode()
			if err != nil {
				continue
			}
			out[s.wireKey] = json.RawMessage(b)
		}
		ctx.dirtySignals.clear()
		ctx.queue.mu.Lock()
		if ctx.queue.signals == nil {
			ctx.queue.signals = make(map[string]any, len(out))
		}
		for k, v := range out {
			ctx.queue.signals[k] = v
		}
		ctx.queue.mu.Unlock()
	}
	ctx.queue.notify()
}

// injectSignals applies signals from a request body into the bound *C's
// Signal[T] fields by wire key.
func injectSignals(ctx *Ctx, sigs map[string]any) {
	for slot, ref := range ctx.signalRefs {
		if ctx.desc.signalSlots[slot].kind != kindSignal {
			continue
		}
		key := ctx.desc.signalSlots[slot].wireKey
		if v, ok := sigs[key]; ok {
			ref.decodeRaw(v)
		}
	}
}

func (a *App) handleSSEClose(w http.ResponseWriter, r *http.Request) {
	maxBody := a.cfg.maxRequestBody
	if maxBody == 0 {
		maxBody = 4096
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tabID := strings.TrimSpace(string(body))
	if ctx, err := a.getCtx(tabID); err == nil {
		if ctx.session != nil && a.sessionFromRequest(r) != ctx.session {
			return
		}
		a.disposeCtx(ctx)
		a.unregisterCtx(tabID)
	}
}

// sweepExpiredContexts periodically disposes Ctxs that haven't been touched
// (no SSE event, action, or page-render) for longer than contextTTL.
func (a *App) sweepExpiredContexts() {
	interval := a.cfg.contextTTL / 2
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopSweep:
			return
		case <-ticker.C:
			a.removeExpiredContexts()
		}
	}
}

func (a *App) removeExpiredContexts() {
	cutoff := time.Now().Add(-a.cfg.contextTTL).UnixNano()
	a.contextRegistryMutex.Lock()
	expired := make([]*Ctx, 0)
	for id, c := range a.contextRegistry {
		if c.lastAccess.Load() < cutoff {
			expired = append(expired, c)
			delete(a.contextRegistry, id)
		}
	}
	a.contextRegistryMutex.Unlock()
	for _, c := range expired {
		a.disposeCtx(c)
	}
}

// disposeCtx closes the ctx and runs Dispose if defined.
func (a *App) disposeCtx(ctx *Ctx) {
	ctx.mu.Lock()
	if ctx.disposed {
		ctx.mu.Unlock()
		return
	}
	ctx.disposed = true
	close(ctx.doneChan)
	if ctx.queue != nil {
		ctx.queue.mu.Lock()
		ctx.queue.disposed = true
		ctx.queue.mu.Unlock()
	}
	ctx.mu.Unlock()

	if ctx.desc != nil && ctx.desc.hasDispose && ctx.cmpVal != nil {
		defer func() {
			if rec := recover(); rec != nil {
				a.logErr(ctx, "Dispose panicked: %v", rec)
			}
		}()
		m := reflect.ValueOf(ctx.cmpVal).MethodByName("Dispose")
		if m.IsValid() {
			m.Call([]reflect.Value{reflect.ValueOf(ctx)})
		}
	}
}
