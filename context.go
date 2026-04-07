package via

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/go-via/via/h"
)

// Ctx is the execution context — created per request, passed to view and action functions.
type Ctx struct {
	mux          sync.RWMutex
	id           string
	routeParams  map[string]string
	cmp          *Cmp
	patchChan    chan patch
	doneChan     chan struct{}
	signalValues map[string]*signalValue
	stateMod     bool
	disposed     bool
	initialized  bool
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
	select {
	case ctx.patchChan <- p:
	default:
		ctx.cmp.app.logWarn(ctx, "patch dropped: channel buffer full")
	}
}

func (ctx *Ctx) markStateModified() {
	ctx.stateMod = true
}

// Done returns a channel that is closed when the context is disposed.
func (ctx *Ctx) Done() <-chan struct{} {
	return ctx.doneChan
}
