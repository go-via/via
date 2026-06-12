package via

import (
	"encoding/json"
	"maps"
	"strings"

	"github.com/go-via/via/h"
)

// Imperative client-push helpers on *Ctx: ways for the server to tell
// the browser "patch these signals / morph these elements / run this JS
// / navigate / alert / reload" at the next flush. [Ctx.Redirect] and
// [Ctx.Notify] are convenience wrappers over the same patch queue; all of
// these queue side effects directly rather than returning errors.

// Patch groups the low-level wire-push primitives — push a signal value
// for a key not bound to a typed Signal[T] field, or morph an arbitrary
// element fragment into the live DOM. Reach for these only when the
// typed path ([Signal.Write], View re-render) doesn't fit:
//
//	ctx.Patch().Signal("_picoTheme", "purple")           // ad-hoc client signal
//	ctx.Patch().Signals(map[string]any{"a": 1, "b": 2})  // batched merge
//	ctx.Patch().Element(h.Div(h.ID("toast"), ...))       // single morph
//	ctx.Patch().Elements(div1, div2)                     // variadic morph batch
//
// The Patch handle is allocated eagerly in newCtx; ctx.Patch() is a
// plain field load with no allocation. Mirrors how *CtxR is cached.
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
	// Mirror into the resync tracker: the queue empties on a successful
	// drain, but a frame drained onto a dying socket may never reach the
	// client — the tracker keeps the last pushed value per key so a
	// reconnect resync can re-ship it (see resyncSignals in sse.go).
	if p.ctx.pushedSignals == nil {
		p.ctx.pushedSignals = make(map[string]any, len(values))
	}
	maps.Copy(p.ctx.pushedSignals, values)
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

// Notify shows message as a transient notification. The default (and
// currently only) surface is a small, styled, non-blocking toast that slides
// into a fixed overlay and auto-dismisses after a few seconds. It is the
// default surface for recovered action-handler panics, and the "show a
// quick notice and move on" sugar for app code. Zero setup: the first
// toast on a page injects its own <style> and #via-toast-root container,
// later toasts reuse them and stack.
//
// The message is rendered via textContent (never as HTML), and JSON-
// encoded into the snippet so it can neither break out of the JS string
// nor inject markup — Go's json HTML-escaping also neutralises a
// </script> breakout of the surrounding datastar script element.
//
// EXPERIMENTAL: the contract (show a transient message) is stable, but the
// rendered SURFACE — the toast markup, styling, and stacking — may change
// before 1.0; don't depend on the emitted DOM.
func (ctx *Ctx) Notify(message string) {
	if ctx == nil || message == "" {
		return
	}
	if script, ok := buildToastScript(message); ok {
		ctx.ExecScript(script)
	}
}

// buildToastScript wraps message into the self-contained, XSS-safe toast
// snippet (JSON-encoded so arbitrary text — including markup — is inert).
// Shared by [Ctx.Notify] and [App.BroadcastNotify]. ok is false only when the
// message can't be JSON-encoded, which for a string never happens.
func buildToastScript(message string) (string, bool) {
	b, err := json.Marshal(message)
	if err != nil {
		return "", false
	}
	return toastScriptHead + string(b) + toastScriptTail, true
}

// toastScriptHead / toastScriptTail wrap a JSON-encoded message into the
// self-contained toast snippet ctx.Notify emits. It rides ExecScript — the
// same script-frame path the previous alert() used — so there is no
// document change, no new public API, and no CSP posture shift versus the
// alert it replaces. The <style> and container are keyed by element id so
// they are created once per page and reused; the styling is deliberately
// neutral so it reads acceptably on a bare page and under the picocss
// plugin. The message arrives as the trailing call argument.
const (
	toastScriptHead = `(function(m){` +
		`if(!document.getElementById("via-toast-css")){` +
		`var s=document.createElement("style");s.id="via-toast-css";` +
		`s.textContent="#via-toast-root{position:fixed;inset:auto 1rem 1rem auto;` +
		`z-index:2147483647;display:flex;flex-direction:column;gap:.5rem;` +
		`max-width:min(92vw,22rem);pointer-events:none}` +
		`.via-toast{pointer-events:auto;background:#1f2937;color:#fff;` +
		`font:500 .9rem/1.4 system-ui,-apple-system,sans-serif;padding:.7rem .9rem;` +
		`border-radius:.5rem;box-shadow:0 4px 14px rgba(0,0,0,.25);opacity:0;` +
		`transform:translateY(.6rem);transition:opacity .22s ease,transform .22s ease;` +
		`overflow-wrap:anywhere}.via-toast[data-show]{opacity:1;transform:none}";` +
		`document.head.appendChild(s)}` +
		`var root=document.getElementById("via-toast-root");` +
		`if(!root){root=document.createElement("div");root.id="via-toast-root";` +
		`root.setAttribute("role","status");root.setAttribute("aria-live","polite");` +
		`document.body.appendChild(root)}` +
		`var el=document.createElement("div");el.className="via-toast";` +
		`el.textContent=m;root.appendChild(el);` +
		`requestAnimationFrame(function(){el.setAttribute("data-show","")});` +
		`setTimeout(function(){el.removeAttribute("data-show");` +
		`setTimeout(function(){el.remove()},250)},4000)})(`
	toastScriptTail = `)`
)

// Redirect sends a client-side navigation to url at the next flush.
//
// Only http, https, and same-origin relative paths are honoured. URLs
// carrying any other scheme (javascript:, data:, vbscript:, …) or a
// protocol-relative // prefix are dropped and logged — this closes the
// open-redirect / XSS vector when callers interpolate user input into
// the URL (typical ?next= flows).
func (ctx *Ctx) Redirect(url string) {
	if ctx == nil || url == "" || ctx.queue == nil {
		return
	}
	if !safeRedirectURL(url) {
		if ctx.app != nil {
			ctx.app.logErr(ctx, "Redirect: rejected unsafe URL %q", url)
		}
		return
	}
	q := ctx.queue
	q.mu.Lock()
	q.redirect = url
	q.mu.Unlock()
	q.notify()
}

// safeRedirectURL reports whether url is safe for client-side navigation:
// an http/https absolute URL or a same-origin relative path. Browsers
// strip leading whitespace and control characters before resolving the
// scheme, so " javascript:..." must be treated identically to the
// untrimmed form.
func safeRedirectURL(url string) bool {
	trimmed := strings.TrimLeftFunc(url, func(r rune) bool { return r <= ' ' })
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, `\\`) {
		return false
	}
	if i := strings.IndexAny(trimmed, ":/?#"); i >= 0 && trimmed[i] == ':' {
		scheme := strings.ToLower(trimmed[:i])
		return scheme == "http" || scheme == "https"
	}
	return true
}
