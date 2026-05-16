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
	if limit := a.cfg.maxContexts; limit > 0 {
		a.contextRegistryMu.RLock()
		live := len(a.contextRegistry)
		a.contextRegistryMu.RUnlock()
		if live >= limit {
			a.logWarn(nil, "max contexts reached (%d); rejecting page render", limit)
			http.Error(w, "server is at capacity", http.StatusServiceUnavailable)
			return
		}
	}

	cmpVal := reflect.New(d.typ)
	ctx := newCtx(d, cmpVal, genTabID(d.route))
	ctx.app = a
	ctx.session = a.sessionFromRequest(r)
	ctx.w = w
	ctx.r = r

	decodePathParams(cmpVal, r, d)
	decodeQueryParams(cmpVal, r, d)

	if ctx.initFn != nil {
		if err := ctx.initFn(ctx); err != nil {
			a.logErr(ctx, "OnInit: %v", err)
		}
	}

	a.registerCtx(ctx)

	a.writePageDocument(w, ctx, ctx.viewFn(ctx))
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
func flushDirty(ctx *Ctx) {
	if !ctx.stateDirty && !ctx.dirtySignals.any() {
		return
	}

	if ctx.stateDirty {
		buf := getRenderBuf()
		_ = h.Div(h.ID(ctx.id), ctx.viewFn(ctx)).Render(buf)
		ctx.queue.mu.Lock()
		ctx.queue.elements = buf.String()
		ctx.queue.mu.Unlock()
		putRenderBuf(buf)
		ctx.stateDirty = false
	}

	if ctx.dirtySignals.any() {
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
