package via

import (
	"cmp"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-via/via/internal/spec"
	"github.com/starfederation/datastar-go/datastar"
)

// methodNameCanary is the well-known target for verifyMethodNameTrampoline.
// Defined as a method (not a free function) so the boot-time canary can
// pass a bound method value through spec.MethodName and check the result.
type methodNameCanary struct{}

func (methodNameCanary) Probe() {}

// verifyMethodNameTrampoline fails fast at App construction if a Go
// runtime change has broken spec.MethodName's trampoline-name parsing.
// The expected result for a bound method value is the method name minus
// receiver and "-fm" suffix; a regression that returns "" or anything
// else means every on.Click-style binding would silently fail to
// resolve at request time.
func verifyMethodNameTrampoline() {
	got := spec.MethodName(methodNameCanary{}.Probe)
	if got != "Probe" {
		panic("via: MethodName canary failed; got " + strconv.Quote(got) +
			", want \"Probe\". The Go runtime trampoline format may have " +
			"changed — file a bug.")
	}
}

// sigsPool reuses the per-action signals map across requests. json.Unmarshal
// into a non-nil map merges keys, so acquireSigs returns an already-cleared
// map ready to be passed by pointer.
var sigsPool = sync.Pool{
	New: func() any { return make(map[string]any, 8) },
}

func acquireSigs() map[string]any {
	m := sigsPool.Get().(map[string]any)
	clear(m)
	return m
}

func releaseSigs(m map[string]any) {
	if m == nil || len(m) > 256 {
		return // drop outliers so a one-off broadcast doesn't pin a giant map
	}
	sigsPool.Put(m)
}

// handleAction dispatches POST /_action/{cmpID}.{methodName} (or just
// /_action/{methodName} for root). The {id} URL segment encodes both.
func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	// JSON action bodies and multipart upload bodies have very different
	// size profiles; pick the right cap per content-type so file uploads
	// don't trip the JSON-tuned ceiling.
	var maxBody int64
	if isMultipart(r) {
		maxBody = cmp.Or(a.cfg.maxUploadSize, int64(32<<20))
	} else {
		maxBody = cmp.Or(a.cfg.maxRequestBody, int64(1<<20))
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	sigs := acquireSigs()
	released := false
	defer func() {
		if !released {
			releaseSigs(sigs)
		}
	}()

	var (
		form *multipart.Form
		err  error
	)
	if isMultipart(r) {
		// Memory cap for buffered text fields — file parts spill to disk.
		form, err = readMultipartSignals(r, maxBody, sigs)
	} else {
		err = datastar.ReadSignals(r, &sigs)
	}
	if err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		// Malformed body / wrong content type — fall through to the
		// tabID="" 404 path below; existing tests rely on that posture.
	}
	tabID, _ := sigs[tabSignalKey].(string)

	ctx, ok := a.getCtx(tabID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if sess := ctx.session.Load(); sess != nil && a.sessionFromRequest(r) != sess {
		http.Error(w, "session mismatch", http.StatusForbidden)
		return
	}

	d := ctx.desc
	slotIdx, ok := d.actionByName[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	slot := &d.actionSlots[slotIdx]

	// Wrap the dispatch in the descriptor's group middleware so a
	// requireAuth (or any group-level guard) checks the request before
	// the action runs — same auth posture as the rendered route.
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runAction(a, ctx, slotIdx, slot, w, r, sigs, form)
	})
	applyMiddleware(d.groupMW, dispatch).ServeHTTP(w, r)
	// runAction has finished by the time ServeHTTP returns. Release the
	// sigs map back to the pool. We deliberately don't null out
	// ctx.lastSignals here — a concurrent action POST on the same tab
	// (serialized via actionMu inside runAction) will have already
	// reassigned it, and writing nil from this goroutine would race the
	// reassignment. The stale pointer between actions is benign:
	// lastSignals is only read inside an action body, which holds
	// actionMu, so the pre-read assignment is always under the lock.
	released = true
	releaseSigs(sigs)
}

// isMultipart reports whether r carries a multipart/form-data body.
func isMultipart(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "multipart/form-data")
}

func runAction(a *App, ctx *Ctx, slotIdx int, slot *actionSlot,
	w http.ResponseWriter, r *http.Request, sigs map[string]any, form *multipart.Form) {
	// Action latency timing covers the per-tab serialization wait *and*
	// the handler body — the metric reflects the user-perceived time
	// from POST receipt to handler return, which is what an SLO cares
	// about. Recorded in seconds for prom/otel convention.
	started := time.Now()
	m := a.metricsOrNoop()
	defer func() {
		m.Histogram("via.action.latency", time.Since(started).Seconds(), "method", slot.name)
		m.Counter("via.action.total", "method", slot.name)
	}()
	// Serialize per-tab so parallel POSTs to the same ctx don't race
	// on State writes, dirty bits, or Writer/Request assignment.
	ctx.actionMu.Lock()
	defer ctx.actionMu.Unlock()

	ctx.mu.Lock()
	ctx.w = w
	ctx.r = r
	ctx.mu.Unlock()
	defer func() {
		ctx.mu.Lock()
		ctx.w = nil
		ctx.r = nil
		ctx.mu.Unlock()
	}()
	// Every handler entry starts loud — Silent doesn't leak between
	// actions. Atomic store so concurrent reads from user-launched
	// goroutines driving Update → broadcastRender aren't racy.
	ctx.silent.Store(false)
	// flushDirty runs even on panic so state mutated before the panic
	// still reaches the browser alongside the error toast. Placed
	// *before* the recover defer so the recover runs first (defers are
	// LIFO) and turns the panic back into a normal return. If the
	// handler ended in silent mode, drop any accumulated dirty bits so
	// they don't leak into a subsequent loud action's flush.
	defer func() {
		if ctx.silent.Load() {
			ctx.discardDirty()
			return
		}
		flushDirty(ctx)
	}()
	defer func() {
		rec := recover()
		if rec == nil {
			return
		}
		a.logErr(ctx, "action %q panicked: %v", slot.name, rec)
		// Preserve a typed error from panic(err) so a custom
		// WithActionErrorHandler can errors.As / errors.Is it.
		err, ok := rec.(error)
		if !ok {
			err = fmt.Errorf("panic: %v", rec)
		}
		a.dispatchActionError(ctx, err, true)
	}()

	ctx.lastSignals = sigs
	injectSignals(ctx, sigs)
	if form != nil {
		bindFiles(ctx, form)
		defer clearFiles(ctx)
		defer form.RemoveAll()
	}

	if err := ctx.actionFns[slotIdx](ctx); err != nil {
		a.dispatchActionError(ctx, err, false)
	}
}

func (a *App) dispatchActionError(ctx *Ctx, err error, fromPanic bool) {
	if a.cfg.actionErrorHandler != nil {
		a.cfg.actionErrorHandler(ctx, err)
		return
	}
	msg := err.Error()
	if fromPanic {
		msg = "Something went wrong"
	}
	ctx.Toast(msg)
}

// injectSignals applies signals from a request body into the bound *C's
// Signal[T] fields by wire key.
func injectSignals(ctx *Ctx, sigs map[string]any) {
	for slot, ref := range ctx.signalRefs {
		s := ctx.desc.signalSlots[slot]
		if s.kind != kindSignal {
			continue
		}
		if v, ok := sigs[s.wireKey]; ok {
			ref.decodeRaw(v)
		}
	}
}
