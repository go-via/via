package via

import (
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
)

// reflectValue is a local alias so the field declaration on Ctx doesn't
// pull reflect into every file that imports "via" through field access.
type reflectValue = reflect.Value

// Ctx is the per-request execution context. Created on page load, kept alive
// for the lifetime of the SSE stream, passed to View/Init/Action methods.
type Ctx struct {
	id           string // tab id, generated per page request
	app          *App
	desc         *cmpDescriptor
	cmpVal       any           // the bound *C
	cmpReflect   any           // reflect.Value cache (boxed once)
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

	connectOnce sync.Once // guards OnConnect dispatch

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

// Sync explicitly re-renders the view and flushes pending patches.
func (ctx *Ctx) Sync() {
	if ctx == nil {
		return
	}
	ctx.markStateDirty()
	flushDirty(ctx)
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
