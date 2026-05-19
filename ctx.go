package via

import (
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-via/via/h"
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

	// readsMu guards the render-time subscription tracker. lastReads is
	// read by broadcastRender from any goroutine, so a lock is required
	// even though per-ctx renders are serialized through actionMu.
	readsMu       sync.Mutex
	rendering     bool
	inflightReads map[string]struct{}
	lastReads     map[string]struct{}

	// Typed dispatch funcs, bound once at newCtx by extracting each
	// reflect-discovered method as a method value (`cmpVal.Method(i).
	// Interface().(func(*Ctx)…)`). Per-request action/lifecycle calls
	// then go through these direct funcs — no reflect.Value.Call on the
	// hot path. Void-return actions are wrapped to satisfy the unified
	// `func(*Ctx) error` shape; nil means "no such hook".
	viewFn    func(*Ctx) h.H
	initFn    func(*Ctx) error
	connectFn func(*Ctx) error
	disposeFn func(*Ctx)
	actionFns []func(*Ctx) error // indexed by descriptor actionSlot index

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

// Writer returns the http.ResponseWriter for the in-flight request, or
// nil if the caller isn't on the action or page-render goroutine. The
// pointer is cleared as soon as the synchronous handler returns, so it
// is unsafe to capture from a background goroutine and use later.
func (ctx *Ctx) Writer() http.ResponseWriter {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.w
}

// Request returns the *http.Request for the in-flight request, or nil
// if the caller isn't on the action or page-render goroutine. Same
// lifetime caveat as [Writer]: cleared on handler return, do not
// capture for later use.
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

// markSignalDirty records that slot needs a signal patch on the next
// flush. Synchronized via queue.mu so Set on a typed Signal handle is
// safe from any goroutine (including user-launched ones reaching the
// Ctx through Done/Stream).
func (ctx *Ctx) markSignalDirty(slot uint16) {
	if ctx.queue == nil {
		return
	}
	ctx.queue.mu.Lock()
	ctx.dirtySignals.set(int(slot))
	ctx.queue.mu.Unlock()
	ctx.queue.notify()
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

// markStateDirty records that the view needs a re-render on the next
// flush. Synchronized via queue.mu so scope.User/scope.App writes from
// a user goroutine don't race with the SSE drain loop.
func (ctx *Ctx) markStateDirty() {
	if ctx.queue == nil {
		return
	}
	ctx.queue.mu.Lock()
	ctx.stateDirty = true
	ctx.queue.mu.Unlock()
	ctx.queue.notify()
}

// beginRender opens a "currently rendering" window during which every
// trackRead call records its wireKey into the in-flight subscription
// set. Paired with endRender, which publishes the set so broadcastRender
// can read it from another goroutine.
func (ctx *Ctx) beginRender() {
	ctx.readsMu.Lock()
	ctx.rendering = true
	ctx.inflightReads = make(map[string]struct{})
	ctx.readsMu.Unlock()
}

// endRender closes the render window and publishes the inflight read
// set as the ctx's current subscription set.
func (ctx *Ctx) endRender() {
	ctx.readsMu.Lock()
	ctx.rendering = false
	ctx.lastReads = ctx.inflightReads
	ctx.inflightReads = nil
	ctx.readsMu.Unlock()
}

// trackRead records that the current render touched key. No-op outside
// a beginRender/endRender window so action handlers and lifecycle hooks
// don't accidentally subscribe.
func (ctx *Ctx) trackRead(key string) {
	ctx.readsMu.Lock()
	if ctx.rendering {
		ctx.inflightReads[key] = struct{}{}
	}
	ctx.readsMu.Unlock()
}

// subscribed reports whether the ctx's most recently published render
// read key. A ctx that has never completed a render returns false — its
// first render will read fresh state anyway, so skipping the broadcast
// is correct.
func (ctx *Ctx) subscribed(key string) bool {
	ctx.readsMu.Lock()
	_, ok := ctx.lastReads[key]
	ctx.readsMu.Unlock()
	return ok
}
