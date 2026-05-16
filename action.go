package via

import (
	"cmp"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/starfederation/datastar-go/datastar"
)

// methodNameCache memoises MethodName results by trampoline PC so the
// reflect-light parse (runtime.FuncForPC, two string scans) only runs the
// first time we see a given bound method. PC is stable per (type, method)
// pair, so the cache is safe across compositions.
var methodNameCache sync.Map // map[uintptr]string

// MethodName resolves a bound method value (like `c.Inc`) to its method
// name. Method values in Go are closures; runtime.FuncForPC on the value's
// PC returns the trampoline name in the form
//
//	pkg.(*Counter).Inc-fm
//
// We strip the package, receiver, and -fm suffix to recover "Inc". Returns
// "" if the function value is not recognizable as a bound method.
func MethodName(fn any) string {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		return ""
	}
	pc := v.Pointer()
	if cached, ok := methodNameCache.Load(pc); ok {
		return cached.(string)
	}
	fnPC := runtime.FuncForPC(pc)
	if fnPC == nil {
		return ""
	}
	full := fnPC.Name()
	// Trim trailing "-fm" (Go's bound-method suffix).
	full = strings.TrimSuffix(full, "-fm")
	// Last dot separates receiver/package from method name.
	if i := strings.LastIndex(full, "."); i >= 0 {
		full = full[i+1:]
	}
	methodNameCache.Store(pc, full)
	return full
}

// TriggerOption is consumed by the on/* sub-package to layer extra
// behaviour onto a binding (debounce, throttle, key filters, etc.).
type TriggerOption func(*TriggerSpec)

// TriggerSpec is the resolved configuration of one event binding. The on/*
// package exposes a builder; via owns the type so consumers don't need to
// reach across packages.
//
// Method is a bound method value of either `func(*Ctx)` or `func(*Ctx) error`.
// The runtime resolves the method name via MethodName and dispatches via
// reflect — both shapes are accepted; nothing else is.
type TriggerSpec struct {
	Event     string // "click", "input", "submit", …
	Method    any    // bound method value — see godoc above
	Debounce  string // e.g. "200ms"
	Throttle  string
	Modifiers []string // e.g. ["prevent", "stop"]
	KeyFilter string   // e.g. "Enter" for on:keydown

	// Pre is a list of JS statements to run synchronously before the
	// @post(...) call fires. Used by on.SetSignal to bundle a typed
	// signal write into the same trigger.
	Pre []string
}

// AppendPre adds a JS statement that will run before the action POST.
// Used by on.SetSignal and other helpers in the on/* package; the
// statements run in insertion order.
func (s *TriggerSpec) AppendPre(stmt string) {
	if stmt == "" {
		return
	}
	s.Pre = append(s.Pre, stmt)
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

	maxBody := cmp.Or(a.cfg.maxRequestBody, int64(1<<20))
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
		// Reuse maxBody so callers tune one knob.
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
	if ctx.session != nil && a.sessionFromRequest(r) != ctx.session {
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
	// flushDirty runs even on panic so state mutated before the panic
	// still reaches the browser alongside the error toast. Placed
	// *before* the recover defer so the recover runs first (defers are
	// LIFO) and turns the panic back into a normal return.
	defer flushDirty(ctx)
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
		var re *RedirectError
		if errors.As(err, &re) {
			ctx.Redirect(re.URL)
			return
		}
		var te *ToastError
		if errors.As(err, &te) {
			ctx.Toast(te.Message)
			return
		}
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
