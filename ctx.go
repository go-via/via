package via

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
)

func contextWithCSPNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, cspNonceKey{}, nonce)
}

// reflectValue is a local alias so the field declaration on Ctx doesn't
// pull reflect into every file that imports "via" through field access.
type reflectValue = reflect.Value

// Ctx is the per-request execution context. Created on page load, kept alive
// for the lifetime of the SSE stream, passed to View/Init/Action methods.
type Ctx struct {
	id           string // tab id, generated per page request
	app          *App
	desc         *cmpDescriptor
	cmpVal       any         // the bound *C
	cmpReflect   any         // reflect.Value cache (boxed once)
	signalRefs   []signalRef // indexed by slot
	dirtySignals bitset      // size = len(signalRefs)
	stateDirty   bool        // any State[T] mutated → re-render needed
	queue        *patchQueue
	doneChan     chan struct{}
	disposed     bool
	session      *session
	lastAccess   atomic.Int64

	// localScope backs scope.User[T] when the request had no session
	// (test path or unauth scenarios). Per-Ctx, not shared.
	localScope sync.Map

	// lastSignals holds the most recent signals payload from an action
	// POST so via.DecodeForm can read keys that aren't tracked by typed
	// Signal[T] fields. Reset at request entry.
	lastSignals map[string]any

	cspNonce string // lazily generated per-request CSP nonce

	connectOnce sync.Once // guards OnConnect dispatch

	// actionMu serializes action handlers per-Ctx. Without it, two POSTs
	// for the same tab arriving concurrently race on State writes,
	// dirty bits, and Writer/Request assignment.
	actionMu sync.Mutex

	// reflectArgs is the cached single-element [reflect.ValueOf(ctx)]
	// used as the argument list for Init/View/Action/OnConnect/Dispose
	// reflect.Method.Call. Boxing ctx once and re-using the slice
	// avoids 2 allocations per dispatch.
	reflectArgs [1]reflectValue

	mu sync.Mutex // guards w / r and disposed flag

	w http.ResponseWriter
	r *http.Request
}

// Done returns a channel closed on context disposal (tab close or shutdown).
func (ctx *Ctx) Done() <-chan struct{} { return ctx.doneChan }

// ID returns the tab id (the wire key for via_tab).
func (ctx *Ctx) ID() string { return ctx.id }

// Writer returns the http.ResponseWriter for the current action, or nil
// outside of action scope.
func (ctx *Ctx) Writer() http.ResponseWriter {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.w
}

// Request returns the *http.Request for the current action, or nil outside
// of action scope.
func (ctx *Ctx) Request() *http.Request {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.r
}

// Session returns the per-browser session value bag. Survives tab close;
// expires per WithSessionTTL.
func (ctx *Ctx) Session() *session { return ctx.session }

func (ctx *Ctx) touch() {
	ctx.lastAccess.Store(nowNano())
}

func (ctx *Ctx) markSignalDirty(slot uint16) {
	if ctx == nil {
		return
	}
	ctx.dirtySignals.set(int(slot))
	if ctx.queue != nil {
		ctx.queue.notify()
	}
}

// MarkDirty schedules a view re-render on the next flush. Use from external
// reactive primitives (e.g. scope.User[T]) that mutate state outside the
// composition struct's own State[T] handles.
func MarkDirty(ctx *Ctx) {
	if ctx == nil {
		return
	}
	ctx.markStateDirty()
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

// ExecScriptf is ExecScript with fmt-style formatting:
//
//	ctx.ExecScriptf("alert(%q)", err.Error())
func (ctx *Ctx) ExecScriptf(format string, args ...any) {
	if ctx == nil || format == "" {
		return
	}
	enqueueScript(ctx, sprintfFmt(format, args...))
}

// Redirect sends a client-side navigation to url at the next flush.
func (ctx *Ctx) Redirect(url string) {
	if ctx == nil || url == "" || ctx.queue == nil {
		return
	}
	q := ctx.queue
	q.mu.Lock()
	q.redirect = url
	q.hasRedir = true
	q.mu.Unlock()
	q.notify()
}

// CSPNonce returns a per-request cryptographically-random base64
// nonce suitable for use with strict Content-Security-Policy headers.
// The same value is returned on every call within one request, so
// plugins and the page render share one nonce.
//
// For strict CSP enforcement, install a middleware that pre-generates
// the nonce and threads it through both the response header and the
// request context, e.g.:
//
//	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
//	    n := via.NewCSPNonce()
//	    w.Header().Set("Content-Security-Policy",
//	        "script-src 'self' 'nonce-"+n+"'")
//	    next.ServeHTTP(w, via.RequestWithCSPNonce(r, n))
//	})
//
// Without that middleware, CSPNonce returns a random per-request value
// that the browser will not honor (no matching CSP header) — useful in
// development, useless in production.
func (ctx *Ctx) CSPNonce() string {
	if ctx == nil {
		return ""
	}
	if ctx.cspNonce != "" {
		return ctx.cspNonce
	}
	// Pull from request context if a middleware put one there.
	if ctx.r != nil {
		if v, ok := ctx.r.Context().Value(cspNonceKey{}).(string); ok && v != "" {
			ctx.cspNonce = v
			return v
		}
	}
	ctx.cspNonce = genCSPNonce()
	return ctx.cspNonce
}

// cspNonceKey is the unexported r.Context() key used to thread a
// pre-generated nonce from middleware into the rendered page.
type cspNonceKey struct{}

// NewCSPNonce returns a fresh 16-byte base64url nonce. Callers
// installing a strict-CSP middleware use this to keep the value
// consistent across the response header and the rendered HTML.
func NewCSPNonce() string { return genCSPNonce() }

// RequestWithCSPNonce returns r with nonce stored in its context so
// downstream renderPage can find it via Ctx.CSPNonce().
func RequestWithCSPNonce(r *http.Request, nonce string) *http.Request {
	return r.WithContext(contextWithCSPNonce(r.Context(), nonce))
}

// Sync explicitly re-renders the view and flushes pending patches.
func (ctx *Ctx) Sync() {
	if ctx == nil {
		return
	}
	ctx.markStateDirty()
	flushDirty(ctx)
}

// PendingRedirect returns the URL queued by Redirect (if any) without
// draining it. Reserved for tests that drive actions through
// test.NewCtx and need to assert that an action queued a redirect.
func (ctx *Ctx) PendingRedirect() string {
	if ctx == nil || ctx.queue == nil {
		return ""
	}
	ctx.queue.mu.Lock()
	defer ctx.queue.mu.Unlock()
	if !ctx.queue.hasRedir {
		return ""
	}
	return ctx.queue.redirect
}

// PendingScripts returns the JavaScript queued by ExecScript /
// ExecScriptf, without draining it. Reserved for tests.
func (ctx *Ctx) PendingScripts() string {
	if ctx == nil || ctx.queue == nil {
		return ""
	}
	ctx.queue.mu.Lock()
	defer ctx.queue.mu.Unlock()
	return ctx.queue.scripts.String()
}

// PendingSignals returns a snapshot of the signals queued for the
// next flush, without draining them. Reserved for tests.
func (ctx *Ctx) PendingSignals() map[string]any {
	if ctx == nil || ctx.queue == nil {
		return nil
	}
	ctx.queue.mu.Lock()
	defer ctx.queue.mu.Unlock()
	if len(ctx.queue.signals) == 0 {
		return nil
	}
	out := make(map[string]any, len(ctx.queue.signals))
	for k, v := range ctx.queue.signals {
		out[k] = v
	}
	return out
}

func (ctx *Ctx) markStateDirty() {
	ctx.stateDirty = true
	if ctx.queue != nil {
		ctx.queue.notify()
	}
}

// bitset is a small fixed-size dirty tracker. We use uint64 words; the
// fixed size is set at descriptor build time.
type bitset struct {
	words []uint64
}

func newBitset(n int) bitset {
	if n == 0 {
		return bitset{}
	}
	return bitset{words: make([]uint64, (n+63)/64)}
}

func (b *bitset) set(i int) {
	if i < 0 || i >= len(b.words)*64 {
		return
	}
	b.words[i/64] |= 1 << (i % 64)
}

func (b *bitset) get(i int) bool {
	if i < 0 || i >= len(b.words)*64 {
		return false
	}
	return b.words[i/64]&(1<<(i%64)) != 0
}

func (b *bitset) clear() {
	for i := range b.words {
		b.words[i] = 0
	}
}

func (b *bitset) any() bool {
	for _, w := range b.words {
		if w != 0 {
			return true
		}
	}
	return false
}
