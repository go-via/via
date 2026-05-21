package via

import (
	"encoding/json"
	"html/template"
	"maps"
	"net/http"
	"reflect"

	"github.com/go-via/via/h"
)

// renderPage handles GET on a Mount-ed route. Allocates a fresh *C, decodes
// path params + initial signal values, optionally calls OnInit, renders the
// view inside the HTML5 envelope.
func (a *App) renderPage(d *cmpDescriptor, w http.ResponseWriter, r *http.Request) {
	cmpVal := reflect.New(d.typ)
	ctx := newCtx(d, cmpVal, genTabID(d.route))
	ctx.app = a
	ctx.session = a.sessionFromRequest(r)
	ctx.mu.Lock()
	ctx.w = w
	ctx.r = r
	ctx.mu.Unlock()
	// Writer / Request are scoped to the synchronous render only — any
	// goroutine the user launches from OnInit must not see a dangling
	// reference to a writer that's already been released back to the
	// server. Mirrors the same clear in runAction.
	defer func() {
		ctx.mu.Lock()
		ctx.w = nil
		ctx.r = nil
		ctx.mu.Unlock()
	}()

	decodePathParams(cmpVal, r, d)
	decodeQueryParams(cmpVal, r, d)

	if ctx.initFn != nil {
		// Symmetric with OnConnect / OnDispose (see sse.go, runtime.go):
		// a panicking OnInit must not propagate up through renderPage
		// without being logged. Without this guard the only backstop is
		// the user's Recover middleware (or http.Server's default panic
		// handler) — meaning the panic message reaches the wire as a 500
		// HTML body instead of as a structured log line.
		func() {
			defer recoverLog(ctx, "OnInit")
			if err := ctx.initFn(ctx); err != nil {
				a.logErr(ctx, "OnInit: %v", err)
			}
		}()
	}

	// Cap check is fused with the registry insert so two concurrent
	// renders can't both observe live==limit-1 and both proceed.
	if !a.tryRegisterCtx(ctx, a.cfg.maxContexts) {
		a.logWarn(nil, "max contexts reached (%d); rejecting page render", a.cfg.maxContexts)
		http.Error(w, "server is at capacity", http.StatusServiceUnavailable)
		return
	}

	ctx.beginRender()
	body := ctx.viewFn(ctx.readView())
	ctx.endRender()
	a.writePageDocument(w, ctx, body)
	a.metricsOrNoop().Counter("via.render.total", "route", d.route)
}

func (a *App) writePageDocument(w http.ResponseWriter, ctx *Ctx, body h.H) {
	a.appSignalsMu.RLock()
	// Size hint: via_tab + every app signal + every typed signal slot.
	// Map auto-grows beyond this if scope handles add more, but a
	// correct hint avoids the rehash chain on the common path.
	initialSigs := make(map[string]any, 1+len(a.appSignals)+len(ctx.desc.signalSlots))
	initialSigs[tabSignalKey] = ctx.id
	maps.Copy(initialSigs, a.appSignals)
	a.appSignalsMu.RUnlock()
	for i, s := range ctx.desc.signalSlots {
		if s.kind != kindSignal {
			continue
		}
		v, err := ctx.signalRefs[i].encode()
		if err != nil {
			continue
		}
		initialSigs[s.wireKey] = json.RawMessage(v)
	}

	sigsJSON, err := json.Marshal(initialSigs)
	if err != nil {
		// A plugin pushed an unmarshalable value via RegisterAppSignal,
		// or a typed Signal[T]'s init value can't round-trip. Log so
		// the page render doesn't silently emit empty data-signals.
		a.logErr(ctx, "writePageDocument: json.Marshal initial signals: %v", err)
	}
	head := make([]h.H, 0, 3+len(a.documentHeadIncludes))
	head = append(head,
		h.Meta(h.Data("signals", string(sigsJSON))),
		h.Meta(h.Data("init", "@get('/_sse')")),
		h.Meta(h.Data("init",
			`window.addEventListener('beforeunload',(e)=>{navigator.sendBeacon('/_sse/close','`+template.JSEscapeString(ctx.id)+`');});`)),
	)
	head = append(head, a.documentHeadIncludes...)

	bodyEls := make([]h.H, 0, 1+len(a.documentFootIncludes))
	bodyEls = append(bodyEls, h.Div(h.ID(ctx.id), body))
	bodyEls = append(bodyEls, a.documentFootIncludes...)

	doc := h.HTML5(h.HTML5Props{
		Title:       a.cfg.title,
		Language:    a.cfg.lang,
		Description: a.cfg.description,
		Head:        head,
		Body:        bodyEls,
		HTMLAttrs:   a.documentHTMLAttrs,
	})
	if err := doc.Render(w); err != nil {
		a.logWarn(ctx, "page render write failed: %v", err)
	}
}

// decodeSlots writes raw values from getRaw into every slot's field.
// Empty raw is skipped so missing query params leave the field at its
// zero value. Path params come back non-empty when the route matched
// (the mux wouldn't dispatch otherwise), so the same skip is harmless.
func decodeSlots(elem reflect.Value, slots []kindedSlot, getRaw func(string) string) {
	for _, p := range slots {
		if raw := getRaw(p.name); raw != "" {
			decodeScalarString(fieldByPath(elem, p.fieldPath), p.kind, raw)
		}
	}
}

func decodePathParams(cmpVal reflect.Value, r *http.Request, d *cmpDescriptor) {
	decodeSlots(cmpVal.Elem(), d.paramSlots, r.PathValue)
}

func decodeQueryParams(cmpVal reflect.Value, r *http.Request, d *cmpDescriptor) {
	if len(d.querySlots) == 0 {
		return // skip the r.URL.Query() reparse when nothing wants it
	}
	decodeSlots(cmpVal.Elem(), d.querySlots, r.URL.Query().Get)
}

// flushDirty re-renders the view fragment if any State changed and patches
// any dirty signals to the browser.
//
// The dirty flags are read+cleared under queue.mu before the work runs,
// so a concurrent markStateDirty/markSignalDirty after clear sets the
// flag again and a subsequent notify drives a fresh flush (no missed
// updates, at most an extra render of the latest state).
func flushDirty(ctx *Ctx) {
	ctx.queue.mu.Lock()
	needRender := ctx.stateDirty
	hasSignals := ctx.dirtySignals.any()
	if !needRender && !hasSignals {
		ctx.queue.mu.Unlock()
		return
	}
	ctx.stateDirty = false
	ctx.queue.mu.Unlock()

	if needRender {
		buf := getRenderBuf()
		// View runs without queue.mu held — user code is allowed to
		// call ctx.PatchSignal / ctx.SyncElements, which would deadlock
		// on a re-entrant queue.mu acquisition.
		ctx.beginRender()
		body := ctx.viewFn(ctx.readView())
		ctx.endRender()
		_ = h.Div(h.ID(ctx.id), body).Render(buf)
		ctx.queue.mu.Lock()
		// Prepend the auto re-render so any user-explicit SyncElements
		// patches already queued (e.g. from inside the action body) end
		// up later in the wire frame. Datastar's morph applies patches
		// in document order with last-write-wins per id, so this keeps
		// the user's targeted override the authoritative one.
		ctx.queue.elements = buf.String() + ctx.queue.elements
		ctx.queue.mu.Unlock()
		putRenderBuf(buf)
	}

	if hasSignals {
		// Encode-and-merge directly under the queue lock so we don't
		// have to allocate a staging map only to copy it across the
		// lock boundary. encode() is cheap (scalar paths skip fmt /
		// json entirely), so the extra lock-hold is negligible.
		ctx.queue.mu.Lock()
		if ctx.queue.signals == nil {
			ctx.queue.signals = make(map[string]any)
		}
		for slot, ref := range ctx.signalRefs {
			if !ctx.dirtySignals.get(slot) {
				continue
			}
			b, err := ref.encode()
			if err != nil {
				continue
			}
			ctx.queue.signals[ctx.desc.signalSlots[slot].wireKey] = json.RawMessage(b)
		}
		ctx.dirtySignals.clear()
		ctx.queue.mu.Unlock()
	}
	ctx.queue.notify()
}
