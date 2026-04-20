package via

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-via/via/internal/viaold/h"
	"github.com/starfederation/datastar-go/datastar"
)

type patchType int

const (
	patchTypeElements = iota
	patchTypeSignals
	patchTypeScript
	patchTypeRedirect
)

type patch struct {
	typ     patchType
	content string
}

const maxPendingScriptBytes = 256 * 1024

// patchQueue coalesces outgoing patches so bursts do not lose state. Elements
// use a last-wins slot (each element patch is a full re-render, so only the
// latest is observable). Signals merge by key into a map (latest value wins
// per signal). Scripts concatenate into one buffer with per-script try/catch
// wrapping to preserve order without losing siblings on a single failure.
// Redirects use a last-wins slot and, when set, preempt every other pending
// patch on drain so the client does not apply work to a page about to navigate.
type patchQueue struct {
	mu              sync.Mutex
	pendingElems    string
	hasElems        bool
	pendingSignals  map[string]any
	pendingScripts  strings.Builder
	scriptOverflow  bool
	pendingRedirect string
	hasRedirect     bool
	disposed        bool
	wake            chan struct{}
}

func newPatchQueue() *patchQueue {
	return &patchQueue{wake: make(chan struct{}, 1)}
}

func (q *patchQueue) notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Ctx is the execution context — created per request, passed to view and action functions.
type Ctx struct {
	mux          sync.RWMutex
	id           string
	routeParams  map[string]string
	cmp          *Cmp
	queue        *patchQueue
	doneChan     chan struct{}
	signalValues map[string]*signalValue
	stateMod     bool
	disposed     bool
	session      *session
	lastAccess   atomic.Int64
	actionMu     sync.Mutex

	// w and r hold raw HTTP access during action execution. Read via the
	// Writer / Request accessors, which guard against stale reads from
	// goroutines that outlive the action.
	w http.ResponseWriter
	r *http.Request
}

// Writer returns the raw http.ResponseWriter for the currently executing
// action, or nil if called outside of action scope (for example from a
// goroutine that outlived the action). A nil read is logged at warn level.
func (ctx *Ctx) Writer() http.ResponseWriter {
	ctx.mux.RLock()
	w := ctx.w
	ctx.mux.RUnlock()
	if w == nil && ctx.cmp != nil && ctx.cmp.app != nil {
		ctx.cmp.app.logWarn(ctx, "Writer() called outside action scope; returning nil")
	}
	return w
}

// Request returns the raw *http.Request for the currently executing action,
// or nil if called outside of action scope.
func (ctx *Ctx) Request() *http.Request {
	ctx.mux.RLock()
	r := ctx.r
	ctx.mux.RUnlock()
	if r == nil && ctx.cmp != nil && ctx.cmp.app != nil {
		ctx.cmp.app.logWarn(ctx, "Request() called outside action scope; returning nil")
	}
	return r
}

func (ctx *Ctx) touch() {
	ctx.lastAccess.Store(time.Now().UnixNano())
}

// GetPathParam returns the value of a named path parameter from the request URL.
func (ctx *Ctx) GetPathParam(name string) string {
	ctx.mux.RLock()
	defer ctx.mux.RUnlock()
	return ctx.routeParams[name]
}

// SyncElements sends HTML element patches to the browser immediately.
func (ctx *Ctx) SyncElements(elem ...h.H) {
	b := bytes.NewBuffer(nil)
	for _, el := range elem {
		if el == nil {
			continue
		}
		_ = el.Render(b)
	}
	ctx.sendPatch(patch{patchTypeElements, b.String()})
}

// Redirect navigates the browser to the given URL.
func (ctx *Ctx) Redirect(url string) {
	if url == "" {
		return
	}
	ctx.sendPatch(patch{patchTypeRedirect, url})
}

// ExecScript sends a JavaScript snippet to the browser for execution.
func (ctx *Ctx) ExecScript(s string) {
	if s == "" {
		return
	}
	ctx.sendPatch(patch{patchTypeScript, s})
}

// Sync explicitly re-renders the view and flushes all pending patches to the browser.
func (ctx *Ctx) Sync() {
	ctx.stateMod = true
	ctx.flushPatches()
}

// MarshalAndPatchSignals marshals the given key-value pairs and pushes them
// to the browser as a signal patch. Use this for signals outside via's scope
// (e.g. plugin-owned frontend signals like _picoDarkMode).
func (ctx *Ctx) MarshalAndPatchSignals(signals map[string]any) {
	if len(signals) == 0 {
		return
	}
	out, _ := json.Marshal(signals)
	ctx.sendPatch(patch{patchTypeSignals, string(out)})
}

// Done returns a channel that is closed when the context is disposed.
func (ctx *Ctx) Done() <-chan struct{} {
	return ctx.doneChan
}

func (ctx *Ctx) flushPatches() {
	cmp := ctx.cmp
	if cmp == nil || cmp.viewFn == nil {
		return
	}

	if ctx.stateMod {
		ctx.stateMod = false
		elemsPatch := &bytes.Buffer{}
		wrapped := h.Div(h.ID(ctx.id), cmp.viewFn(ctx))
		if err := wrapped.Render(elemsPatch); err != nil {
			return
		}
		ctx.sendPatch(patch{patchTypeElements, elemsPatch.String()})
	}

	updatedSigs := ctx.prepareSignalsForPatch()
	if len(updatedSigs) != 0 {
		outgoingSigs, _ := json.Marshal(updatedSigs)
		ctx.sendPatch(patch{patchTypeSignals, string(outgoingSigs)})
	}
}

func (ctx *Ctx) hasSignalChanges() bool {
	for _, sv := range ctx.signalValues {
		if sv.changed {
			return true
		}
	}
	return false
}

func (ctx *Ctx) sendPatch(p patch) {
	q := ctx.queue
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.disposed {
		return
	}
	switch p.typ {
	case patchTypeElements:
		q.pendingElems = p.content
		q.hasElems = true
	case patchTypeSignals:
		var incoming map[string]any
		if err := json.Unmarshal([]byte(p.content), &incoming); err != nil {
			ctx.cmp.app.logErr(ctx, "signal patch unmarshal failed: %v", err)
			return
		}
		if q.pendingSignals == nil {
			q.pendingSignals = make(map[string]any, len(incoming))
		}
		for k, v := range incoming {
			q.pendingSignals[k] = v
		}
	case patchTypeScript:
		const prefix = "try{"
		const suffix = "}catch(e){console.error(e)};"
		if q.pendingScripts.Len()+len(prefix)+len(p.content)+len(suffix) > maxPendingScriptBytes {
			q.scriptOverflow = true
			ctx.cmp.app.logErr(ctx, "script buffer overflow: patch dropped")
			break
		}
		q.pendingScripts.WriteString(prefix)
		q.pendingScripts.WriteString(p.content)
		q.pendingScripts.WriteString(suffix)
	case patchTypeRedirect:
		q.pendingRedirect = p.content
		q.hasRedirect = true
	}
	q.notify()
}

func (ctx *Ctx) markStateModified() {
	ctx.stateMod = true
}

func (ctx *Ctx) injectSignals(sigs map[string]any) {
	if sigs == nil {
		return
	}
	for incomingID, val := range sigs {
		if incomingID == "via_tab" {
			continue
		}
		for _, sigMap := range []map[string]any{ctx.cmp.app.signals, ctx.cmp.signals} {
			found := false
			for sigID, sig := range sigMap {
				if sm, ok := sig.(signalMeta); ok && sm.displayID() == incomingID {
					ctx.signalValues[sigID] = &signalValue{raw: sm.coerce(val)}
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
}

func (ctx *Ctx) prepareSignalsForPatch() map[string]any {
	updatedSigs := make(map[string]any)
	for _, sigMap := range []map[string]any{ctx.cmp.app.signals, ctx.cmp.signals} {
		for sigID, sig := range sigMap {
			if sm, ok := sig.(signalMeta); ok {
				if sv, exists := ctx.signalValues[sigID]; exists && sv.changed {
					updatedSigs[sm.displayID()] = sv.raw
					sv.changed = false
				}
			}
		}
	}
	return updatedSigs
}

// Page registers a route and its associated page handler.
func (a *App) Page(route string, initCmpFn func(cmp *Cmp)) {
	a.pageWithOptions(route, initCmpFn, nil, a.layoutFn)
}

func (a *App) pageWithOptions(route string, initCmpFn func(cmp *Cmp), groupMW []Middleware, layoutFn func(cmp *Cmp)) {
	var cmp *Cmp
	// Definition phase: run once at startup to register page
	func() {
		defer func() {
			if err := recover(); err != nil {
				a.logPanic("failed to register page with init func that panics: %v", err)
				panic(err)
			}
		}()

		if layoutFn != nil {
			// Layout wraps the page: shared action/signal maps
			layoutCmp := &Cmp{
				app:       a,
				route:     route,
				actionFns: make(map[string]func(ctx *Ctx) error),
				signals:   make(map[string]any),
			}
			pageCmp := &Cmp{
				app:       a,
				route:     route,
				actionFns: layoutCmp.actionFns,
				signals:   layoutCmp.signals,
			}
			initCmpFn(pageCmp)
			if pageCmp.viewFn == nil {
				panic("composition has no view")
			}
			contentID := "via_content_" + genRandID()
			layoutCmp.contentFn = func(ctx *Ctx) h.H {
				return h.Div(h.ID(contentID), pageCmp.viewFn(ctx))
			}
			layoutFn(layoutCmp)
			if layoutCmp.viewFn == nil {
				panic("layout has no view")
			}
			layoutCmp.components = append(layoutCmp.components, pageCmp)
			cmp = layoutCmp
		} else {
			cmp = &Cmp{
				app:       a,
				route:     route,
				actionFns: make(map[string]func(ctx *Ctx) error),
				signals:   make(map[string]any),
			}
			initCmpFn(cmp)
			if cmp.viewFn == nil {
				panic("composition has no view")
			}
		}

		// Call view during definition phase to run any side effects
		defCtx := &Ctx{
			cmp:          cmp,
			queue:        newPatchQueue(),
			doneChan:     make(chan struct{}),
			signalValues: initSignalValues(a, cmp),
		}
		cmp.viewFn(defCtx)
		// Run component init during definition phase
		for _, comp := range cmp.components {
			if comp.initFn != nil {
				comp.initFn(defCtx)
			}
		}
	}()

	paramNames := extractParamNames(route)

	a.mux.HandleFunc("GET "+route, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.logDebug(nil, "GET %s", r.URL.String())
		if r.URL.Path == "/favicon.ico" ||
			strings.HasPrefix(r.URL.Path, "/.well-known/") ||
			strings.HasSuffix(r.URL.Path, ".js.map") {
			return
		}

		// Build middleware chain: global → group
		mwChain := append([]Middleware{}, a.middleware...)
		mwChain = append(mwChain, groupMW...)

		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			params := make(map[string]string, len(paramNames))
			for _, name := range paramNames {
				params[name] = r.PathValue(name)
			}
			id := fmt.Sprintf("%s_/%s", route, genRandID())
			ctx := &Ctx{
				id:           id,
				routeParams:  params,
				cmp:          cmp,
				queue:        newPatchQueue(),
				doneChan:     make(chan struct{}),
				signalValues: initSignalValues(a, cmp),
				session:      sessionFromRequest(r),
			}
			ctx.touch()
			a.registerCtx(id, ctx)

			if cmp.initFn != nil {
				cmp.initFn(ctx)
			}
			for _, comp := range cmp.components {
				if comp.initFn != nil {
					comp.initFn(ctx)
				}
			}

			bodyElements := []h.H{h.Div(h.ID(id), cmp.viewFn(ctx))}

			headElements := []h.H{}
			initialSigs := map[string]any{"via_tab": id}
			for _, sigMap := range []map[string]any{a.signals, cmp.signals} {
				for sigID, sig := range sigMap {
					if sm, ok := sig.(signalMeta); ok && !sm.hasError() {
						if sv, ok := ctx.signalValues[sigID]; ok {
							initialSigs[sm.displayID()] = sm.rawValueOf(sv.raw)
						} else {
							initialSigs[sm.displayID()] = sm.initialRawValue()
						}
					}
				}
			}
			// Reset changed flags so the first action doesn't re-send
			// values that are already in the initial HTML.
			for _, sv := range ctx.signalValues {
				sv.changed = false
			}
			initialSigsJSON, _ := json.Marshal(initialSigs)
			headElements = append(headElements,
				h.Meta(h.Data("signals", string(initialSigsJSON))),
			)
			headElements = append(headElements, a.documentHeadIncludes...)
			headElements = append(headElements,
				h.Meta(h.Data("init", "@get('/_sse')")),
				h.Meta(h.Data("init", fmt.Sprintf(`window.addEventListener('beforeunload', (evt) => {
				navigator.sendBeacon('/_sse/close', '%s');});`, id))),
			)
			bodyElements = append(bodyElements, a.documentFootIncludes...)
			view := h.HTML5(h.HTML5Props{
				Title:     a.cfg.title,
				Head:      headElements,
				Body:      bodyElements,
				HTMLAttrs: a.documentHTMLAttrs,
			})
			_ = view.Render(w)
		})

		runMiddleware(mwChain, w, r, final)
	}))
}

func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)
	cID, _ := sigs["via_tab"].(string)

	ctx, err := a.getCtx(cID)
	if err != nil {
		a.logErr(nil, "sse stream failed to start: %v", err)
		return
	}
	if ctx.session != sessionFromRequest(r) {
		a.logErr(nil, "sse stream failed: session mismatch for ctx '%s'", cID)
		return
	}
	ctx.touch()

	sse := datastar.NewSSE(w, r, datastar.WithCompression(datastar.WithBrotli(datastar.WithBrotliLevel(5))))

	a.logDebug(ctx, "SSE connection established")

	var heartbeat <-chan time.Time
	if a.cfg.sseHeartbeat > 0 {
		t := time.NewTicker(a.cfg.sseHeartbeat)
		defer t.Stop()
		heartbeat = t.C
	}

	for {
		select {
		case <-sse.Context().Done():
			return
		case <-ctx.doneChan:
			return
		case <-heartbeat:
			if err := sse.PatchSignals([]byte("{}")); err != nil {
				return
			}
			ctx.touch()
		case <-ctx.queue.wake:
			if err := drainPatchQueue(sse, ctx); err != nil {
				return
			}
			ctx.touch()
		}
	}
}

func drainPatchQueue(sse *datastar.ServerSentEventGenerator, ctx *Ctx) error {
	q := ctx.queue
	q.mu.Lock()
	elems, hasElems := q.pendingElems, q.hasElems
	signals := q.pendingSignals
	scripts := q.pendingScripts.String()
	redirect, hasRedirect := q.pendingRedirect, q.hasRedirect
	overflow := q.scriptOverflow
	q.pendingElems = ""
	q.hasElems = false
	q.pendingSignals = nil
	q.pendingScripts.Reset()
	q.pendingRedirect = ""
	q.hasRedirect = false
	q.scriptOverflow = false
	q.mu.Unlock()

	if hasRedirect {
		return sse.Redirect(redirect)
	}
	if hasElems {
		if err := sse.PatchElements(elems); err != nil {
			return err
		}
	}
	if len(signals) > 0 {
		sigBytes, _ := json.Marshal(signals)
		if err := sse.PatchSignals(sigBytes); err != nil {
			return err
		}
	}
	if scripts != "" {
		if err := sse.ExecuteScript(scripts); err != nil {
			return err
		}
	}
	if overflow {
		_ = sse.ExecuteScript(`console.error("via: script buffer overflowed; some updates were dropped")`)
	}
	return nil
}

func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	actionID := r.PathValue("id")
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)
	cID, _ := sigs["via_tab"].(string)
	ctx, err := a.getCtx(cID)
	if err != nil {
		a.logErr(nil, "action '%s' failed: %v", actionID, err)
		return
	}
	if ctx.session != sessionFromRequest(r) {
		a.logErr(nil, "action '%s' failed: session mismatch for ctx '%s'", actionID, cID)
		return
	}
	ctx.touch()
	cmp := ctx.cmp
	if cmp == nil || cmp.actionFns == nil {
		a.logDebug(ctx, "action '%s' failed: composition not found", actionID)
		return
	}
	actionFn, ok := cmp.actionFns[actionID]
	if !ok {
		a.logDebug(ctx, "action '%s' failed: not found", actionID)
		return
	}

	// Serialize actions per Ctx to prevent data races on W/R, signals, state
	ctx.actionMu.Lock()
	ctx.mux.Lock()
	ctx.w = w
	ctx.r = r
	ctx.mux.Unlock()
	defer func() {
		ctx.mux.Lock()
		ctx.w = nil
		ctx.r = nil
		ctx.mux.Unlock()
		ctx.actionMu.Unlock()
	}()

	defer func() {
		if r := recover(); r != nil {
			a.logErr(ctx, "action '%s' failed: %v", actionID, r)
			ctx.ExecScript(`alert('Something went wrong')`)
		}
	}()

	// Inject signals from the request
	ctx.injectSignals(sigs)

	if err := actionFn(ctx); err != nil {
		msg, _ := json.Marshal(err.Error())
		ctx.ExecScript(`alert(` + string(msg) + `)`)
	}

	// Auto-sync: re-render and flush if state or signals were modified
	if ctx.stateMod || ctx.hasSignalChanges() {
		ctx.flushPatches()
	}
}

func (a *App) handleSSEClose(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		a.logErr(nil, "sse close: failed to read body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	cID := string(body)
	ctx, err := a.getCtx(cID)
	if err != nil {
		a.logErr(ctx, "failed to handle session close: %v", err)
		return
	}
	if ctx.session != sessionFromRequest(r) {
		a.logErr(ctx, "sse close: session mismatch for ctx '%s'", cID)
		return
	}
	a.disposeCtx(ctx)
	a.unregisterCtx(cID)
}

func initSignalValues(app *App, cmp *Cmp) map[string]*signalValue {
	vals := make(map[string]*signalValue, len(app.signals)+len(cmp.signals))
	for id, sig := range app.signals {
		if sm, ok := sig.(signalMeta); ok {
			vals[id] = &signalValue{raw: sm.initialTypedValue()}
		}
	}
	for id, sig := range cmp.signals {
		if sm, ok := sig.(signalMeta); ok {
			vals[id] = &signalValue{raw: sm.initialTypedValue()}
		}
	}
	return vals
}

func extractParamNames(pattern string) []string {
	var names []string
	for _, seg := range strings.Split(pattern, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			names = append(names, seg[1:len(seg)-1])
		}
	}
	return names
}
