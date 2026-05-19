package via

import (
	"encoding/json"
	"fmt"
	"maps"

	"github.com/go-via/via/h"
)

// Imperative client-push helpers on *Ctx: ways for the server to tell
// the browser "patch these signals / morph these elements / run this JS
// / navigate / alert / reload" at the next flush. Pairs with the typed
// [Redirect] / [Toast] sentinel-error intents in action.go — those
// return errors; these queue side effects directly on the patch queue.

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
//
// Multiple SyncElements calls within the same action — and any view
// re-render queued by State mutations earlier in the same action — are
// concatenated, not overwritten. The browser's morph applies each
// element patch independently by ID, so a State write followed by a
// targeted SyncElements both reach the DOM in one SSE frame.
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
	// Append rather than overwrite so we don't silently drop a view
	// fragment already queued by flushDirty or a previous SyncElements.
	ctx.queue.elements += buf.String()
	ctx.queue.mu.Unlock()
	ctx.queue.notify()
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

// ExecScriptf is ExecScript with fmt-style formatting. Use it to splice
// numeric / boolean values; for user-controlled strings prefer
// JSON-encoding so the embedded value parses unambiguously as a JS
// string literal — Go's %q diverges from JS string syntax in subtle
// ways (\a, some \u forms). For an alert with arbitrary text, see Toast.
//
//	ctx.ExecScriptf("location.href = '/users/%d'", id)
func (ctx *Ctx) ExecScriptf(format string, args ...any) {
	if ctx == nil || format == "" {
		return
	}
	enqueueScript(ctx, fmt.Sprintf(format, args...))
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
// PatchSignal to drive a client-side notice signal instead.
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
