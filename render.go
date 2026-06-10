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
	ctx := newCtx(a, d, cmpVal, genTabID(d.route))
	ctx.session.Store(a.sessionFromRequest(r))
	ctx.mu.Lock()
	ctx.w = w
	ctx.r = r
	ctx.mu.Unlock()
	// Capture the document's CSP nonce now, while the page request is in
	// hand, so server-pushed scripts drained over the (later, separate) SSE
	// request can carry the nonce the browser will actually honor.
	ctx.captureCSPNonce(r)
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

	// Cap check is fused with the registry insert so two concurrent
	// renders can't both observe live==limit-1 and both proceed. Runs
	// BEFORE OnInit so an over-capacity (503-bound) request never executes
	// user init work.
	if !a.tryRegisterCtx(ctx, a.cfg.maxContexts) {
		a.logWarn(nil, "max contexts reached (%d); rejecting page render", a.cfg.maxContexts)
		http.Error(w, "server is at capacity", http.StatusServiceUnavailable)
		return
	}

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

	if a.cfg.devChecks {
		// Run the binding check once per descriptor and cache the verdict — the
		// by-value clobber is deterministic per composition type, so a single
		// post-OnInit walk catches it and every later render pays nothing.
		d.bind.once.Do(func() { d.bind.err = validateBindings(ctx, cmpVal, d) })
		if d.bind.err != nil {
			a.logErr(ctx, "%v", d.bind.err)
			http.Error(w, d.bind.err.Error(), http.StatusInternalServerError)
			return
		}
	}

	body, ok := a.renderView(ctx, w)
	if !ok {
		return
	}
	a.writePageDocument(w, ctx, body)
	a.metricsOrNoop().Counter("via.render.total", "route", d.route)
}

// renderView runs the page's view inside the render window, recovering a
// panicking viewFn so it surfaces as a structured via log line plus a
// controlled 500 rather than escaping to the embedding http.Server (naked
// stderr stack, dropped connection). Symmetric with the OnInit / OnConnect
// / OnDispose guards and action recovery. ok is false when the view
// panicked, in which case the 500 has already been written and the caller
// must not write a page document.
func (a *App) renderView(ctx *Ctx, w http.ResponseWriter) (body h.H, ok bool) {
	ctx.beginRender()
	defer ctx.endRender()
	defer func() {
		if rec := recover(); rec != nil {
			a.logErr(ctx, "View panicked: %v", rec)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}()
	return ctx.viewFn(ctx.readView()), true
}

// initialSignals assembles the signal seed for a fresh ctx: via_tab,
// every plugin-registered app signal, and every typed Signal[T] slot's
// current value. Shared by the page document render and the SSE
// re-bootstrap path (recoverSSE), which must seed the same set.
func (a *App) initialSignals(ctx *Ctx) map[string]any {
	a.appSignalsMu.RLock()
	// Size hint: via_tab + every app signal + every typed signal slot.
	// Map auto-grows beyond this if scope handles add more, but a
	// correct hint avoids the rehash chain on the common path.
	sigs := make(map[string]any, 1+len(a.appSignals)+len(ctx.desc.signalSlots))
	sigs[tabSignalKey] = ctx.id
	maps.Copy(sigs, a.appSignals)
	a.appSignalsMu.RUnlock()
	for i, s := range ctx.desc.signalSlots {
		if s.kind != kindSignal {
			continue
		}
		v, err := ctx.signalRefs[i].encode()
		if err != nil {
			continue
		}
		sigs[s.wireKey] = json.RawMessage(v)
	}
	return sigs
}

func (a *App) writePageDocument(w http.ResponseWriter, ctx *Ctx, body h.H) {
	sigsJSON, err := json.Marshal(a.initialSignals(ctx))
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
	if !a.cfg.noReconnect {
		head = append(head, h.Meta(h.Data("init", reconnectInit)))
	}
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
		// A panicking viewFn must not escape: this runs on the action
		// autoflush defer (would drop the action connection) and on the
		// broadcast SyncNow goroutine (would crash the process — no defer
		// stack to fall back on). renderFragment recovers and logs; a
		// panic yields "" — see the empty-frag guard below.
		frag := ctx.app.renderFragment(ctx)
		if frag != "" {
			ctx.queue.mu.Lock()
			// Replace — never append — the auto re-render. Only the newest
			// render is correct, and the drain emits it before the
			// user-explicit elements queue, so a Patch.Elements override
			// still lands later in the wire frame and stays authoritative
			// under datastar's last-write-wins-per-id morph.
			//
			// Guarded on frag != "": a panicking render yields "", which
			// must NOT clobber a previously queued GOOD render that hasn't
			// drained yet (a disconnected tab accumulating renders — one
			// later panic would otherwise erase the last good frame, and
			// the drain's elems != "" guard means nothing ships, leaving
			// the client with no fresh view at all). Replacing only on a
			// non-empty render preserves the last good frame; the signal
			// flush below still proceeds either way.
			ctx.queue.autoElements = frag
			ctx.queue.mu.Unlock()
		}
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

// renderFragment re-renders the view fragment inside the render window,
// recovering a panicking viewFn so an async re-render (action autoflush,
// broadcast SyncNow) surfaces as a structured via log line instead of
// escaping its goroutine — which, on the broadcast path, would crash the
// process. There is no response writer on this path, so unlike renderView
// the only recovery action is to log; the recovered call returns "", which
// the caller treats as a no-op fragment.
func (a *App) renderFragment(ctx *Ctx) string {
	buf := getRenderBuf()
	defer putRenderBuf(buf)
	// View runs without queue.mu held — user code is allowed to call
	// ctx.Patch().Signal / ctx.Patch().Elements, which would deadlock on a
	// re-entrant queue.mu acquisition.
	ctx.beginRender()
	defer ctx.endRender()
	defer func() {
		if rec := recover(); rec != nil {
			a.logErr(ctx, "View panicked: %v", rec)
		}
	}()
	body := ctx.viewFn(ctx.readView())
	if err := h.Div(h.ID(ctx.id), body).Render(buf); err != nil {
		// Consistent with the page-render path (which logs Render errors):
		// return "" rather than a half-written fragment so the empty-frag
		// guard in flushDirty preserves the last good frame instead of
		// shipping truncated HTML the client would morph in as authoritative.
		a.logErr(ctx, "fragment render: %v", err)
		return ""
	}
	return buf.String()
}
