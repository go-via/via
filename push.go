package via

import (
	"encoding/json"
	"maps"

	"github.com/go-via/via/h"
)

// Imperative client-push helpers on *Ctx: ways for the server to tell
// the browser "patch these signals / morph these elements / run this JS
// / navigate / alert / reload" at the next flush. Pairs with the typed
// [Redirect] / [Toast] sentinel-error intents in action.go — those
// return errors; these queue side effects directly on the patch queue.

// Patch groups the low-level wire-push primitives — push a signal value
// for a key not bound to a typed Signal[T] field, or morph an arbitrary
// element fragment into the live DOM. Reach for these only when the
// typed path (Signal[T].Set, View re-render) doesn't fit:
//
//	ctx.Patch.Signal("_picoTheme", "purple")           // ad-hoc client signal
//	ctx.Patch.Signals(map[string]any{"a": 1, "b": 2})  // batched merge
//	ctx.Patch.Element(h.Div(h.ID("toast"), ...))       // single morph
//	ctx.Patch.Elements(div1, div2)                     // variadic morph batch
//
// The Patch handle is allocated eagerly in newCtx; access is a plain
// field load with no allocation. Mirrors how *CtxR is cached.
type Patch struct {
	ctx *Ctx
}

// Signal queues a single signal update keyed by name. Plugins use it to
// push values to client-only signals they own (e.g. picocss's
// "_picoTheme") without going through a typed Signal[T] handle.
// Multiple Signal/Signals calls within the same flush window are merged
// — last write wins per key. Empty key is a no-op.
func (p *Patch) Signal(key string, value any) {
	if key == "" {
		return
	}
	p.Signals(map[string]any{key: value})
}

// Signals queues many signal updates as a single batched merge. Same
// last-wins-per-key semantics as Signal. Empty / nil map is a no-op.
func (p *Patch) Signals(values map[string]any) {
	if p == nil || p.ctx == nil || p.ctx.queue == nil || len(values) == 0 {
		return
	}
	q := p.ctx.queue
	q.mu.Lock()
	if q.signals == nil {
		q.signals = make(map[string]any, len(values))
	}
	maps.Copy(q.signals, values)
	q.mu.Unlock()
	q.notify()
}

// Element pushes a single h.H tree to the client as an element patch at
// the next flush. The element should carry h.ID("…") so the client
// knows where to morph it. Nil element is a no-op.
func (p *Patch) Element(el h.H) {
	if el == nil {
		return
	}
	p.Elements(el)
}

// Elements pushes one or more h.H trees to the client as element patches
// at the next flush. Useful for action-driven, targeted DOM updates
// that bypass the full view re-render. Each element should carry
// h.ID("…") so the client knows where to morph it.
//
// Multiple Elements calls within the same action — and any view
// re-render queued by State mutations earlier in the same action — are
// concatenated, not overwritten. The browser's morph applies each
// element patch independently by ID, so a State write followed by a
// targeted Elements call both reach the DOM in one SSE frame. Nil
// elements within the variadic list are skipped.
func (p *Patch) Elements(elements ...h.H) {
	if p == nil || p.ctx == nil || p.ctx.queue == nil || len(elements) == 0 {
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
	q := p.ctx.queue
	q.mu.Lock()
	// Append rather than overwrite so we don't silently drop a view
	// fragment already queued by flushDirty or a previous Elements call.
	q.elements += buf.String()
	q.mu.Unlock()
	q.notify()
}

// ExecScript queues a JavaScript snippet for execution on the client at
// the next flush. Use sparingly — most reactivity should flow through
// signals/state rather than imperative scripts.
func (ctx *Ctx) ExecScript(s string) {
	if ctx == nil || s == "" {
		return
	}
	enqueueScript(ctx, s)
}

// Reload tells the browser to reload the current page on the next
// flush. Convenience wrapper for the common "the data changed
// drastically; just refetch" pattern after multi-step actions.
func (ctx *Ctx) Reload() {
	if ctx == nil {
		return
	}
	ctx.ExecScript("location.reload()")
}

// Toast queues a browser alert(message). Sugar for the common
// "show a quick notice and move on" pattern; for richer toasts use
// ctx.Patch.Signal to drive a client-side notice signal instead.
//
// The message is JSON-encoded into the alert call so any user-supplied
// content survives untouched — Go's %q escape rules diverge from
// JavaScript's in a handful of edge cases (e.g. \a), JSON does not.
func (ctx *Ctx) Toast(message string) {
	if ctx == nil || message == "" {
		return
	}
	b, err := json.Marshal(message)
	if err != nil {
		return
	}
	ctx.ExecScript("alert(" + string(b) + ")")
}

// Redirect sends a client-side navigation to url at the next flush.
func (ctx *Ctx) Redirect(url string) {
	if ctx == nil || url == "" || ctx.queue == nil {
		return
	}
	q := ctx.queue
	q.mu.Lock()
	q.redirect = url
	q.mu.Unlock()
	q.notify()
}
