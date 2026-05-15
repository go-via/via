package via

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

// Ctx is the per-request execution context. Created on page load, kept alive
// for the lifetime of the SSE stream, passed to View/OnInit/Action methods.
type Ctx struct {
	id           string // tab id, generated per page request
	app          *App
	desc         *cmpDescriptor
	cmpReflect   reflect.Value // reflect.ValueOf(<bound *C>), boxed once at request entry
	signalRefs   []signalRef   // indexed by slot
	dirtySignals bitset        // size = len(signalRefs)
	stateDirty   bool          // any State[T] mutated → re-render needed
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
	// used as the argument list for OnInit/View/Action/OnConnect/OnDispose
	// reflect.Method.Call. Boxing ctx once and re-using the slice
	// avoids 2 allocations per dispatch.
	reflectArgs [1]reflect.Value

	mu sync.Mutex // guards w / r and disposed flag

	w http.ResponseWriter
	r *http.Request
}

// Done returns a channel closed on context disposal (tab close or shutdown).
func (ctx *Ctx) Done() <-chan struct{} { return ctx.doneChan }

// Disposed reports whether the Ctx has been torn down (tab closed,
// swept by ctx-TTL, or app shutdown). Use it from a long-running
// goroutine to skip expensive work that nobody's going to see:
//
//	for {
//	    if ctx.Disposed() { return }
//	    ...
//	}
//
// Equivalent to a non-blocking <-ctx.Done(), but reads more
// naturally inline.
func (ctx *Ctx) Disposed() bool {
	if ctx == nil {
		return true
	}
	select {
	case <-ctx.doneChan:
		return true
	default:
		return false
	}
}

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

// Cookie returns the value of the named cookie on the in-flight request,
// or "" if the cookie isn't present. Convenience over Request().Cookie
// for the common 80% case where you just want the value:
//
//	consent := ctx.Cookie("cookie_consent")
//
// For full cookie access (Path, Expires, …) use Request().Cookie.
func (ctx *Ctx) Cookie(name string) string {
	r := ctx.Request()
	if r == nil {
		return ""
	}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// SetCookie writes a cookie on the action's response. Convenience over
// http.SetCookie that pulls the response writer off the Ctx; safe to
// call from an action handler. Outside action scope (Writer == nil) it
// is a no-op.
func (ctx *Ctx) SetCookie(c *http.Cookie) {
	if ctx == nil || c == nil {
		return
	}
	w := ctx.Writer()
	if w == nil {
		return
	}
	http.SetCookie(w, c)
}

// DelCookie tells the browser to delete the named cookie by emitting
// a Set-Cookie response header with an empty value, MaxAge=-1, and
// Path="/". For a cookie scoped to a specific path use SetCookie with
// a fully-formed *http.Cookie instead.
func (ctx *Ctx) DelCookie(name string) {
	if ctx == nil || name == "" {
		return
	}
	ctx.SetCookie(&http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1,
	})
}

func (ctx *Ctx) touch() {
	ctx.lastAccess.Store(time.Now().UnixNano())
}

func (ctx *Ctx) markSignalDirty(slot uint16) {
	ctx.dirtySignals.set(int(slot))
	if ctx.queue != nil {
		ctx.queue.notify()
	}
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

// Sync forces a view re-render and flushes pending patches. Marks the
// composition dirty even if nothing changed since the last flush —
// use it when an external (non-State) source of truth changed and you
// need the rendered HTML to reflect it. For "flush whatever's dirty
// now," use Flush.
//
// Safe to call from any goroutine: serialized against in-flight action
// handlers via the per-Ctx action mutex.
func (ctx *Ctx) Sync() {
	if ctx == nil {
		return
	}
	ctx.actionMu.Lock()
	defer ctx.actionMu.Unlock()
	ctx.markStateDirty()
	flushDirty(ctx)
}

// Flush sends any State / Signal mutations queued since the last
// flush, but doesn't force a re-render if nothing is dirty. Use it
// from raw goroutines that just want their Sets to reach the browser
// without paying for an unnecessary re-render. Safe to call from any
// goroutine.
func (ctx *Ctx) Flush() {
	if ctx == nil {
		return
	}
	ctx.actionMu.Lock()
	defer ctx.actionMu.Unlock()
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
	return maps.Clone(ctx.queue.signals)
}

func (ctx *Ctx) markStateDirty() {
	ctx.stateDirty = true
	if ctx.queue != nil {
		ctx.queue.notify()
	}
}
