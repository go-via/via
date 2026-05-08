package via

import (
	"net/http"
	"sync"
	"sync/atomic"
)

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
