package via

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-via/via/h"
	"github.com/starfederation/datastar-go/datastar"
)

type patchType int

const (
	patchTypeElements = iota
	patchTypeSignals
	patchTypeScript
)

type patch struct {
	typ     patchType
	content string
}

// Page registers a route and its associated page handler.
func (a *App) Page(route string, initCmpFn func(cmp *Cmp)) {
	var cmp *Cmp
	// Definition phase: run once at startup to register page
	func() {
		defer func() {
			if err := recover(); err != nil {
				a.logFatal("failed to register page with init func that panics: %v", err)
				panic(err)
			}
		}()
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
		// Call view during definition phase to run any side effects
		defCtx := &Ctx{
			cmp:          cmp,
			patchChan:    make(chan patch, 1),
			doneChan:     make(chan struct{}),
			signalValues: initSignalValues(cmp),
		}
		cmp.viewFn(defCtx)
		// Run component init during definition phase
		for _, comp := range cmp.components {
			if comp.initFn != nil {
				comp.initFn(defCtx)
			}
		}
	}()

	a.mux.HandleFunc("GET "+route, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.logDebug(nil, "GET %s", r.URL.String())
		if strings.Contains(r.URL.Path, "favicon") ||
			strings.Contains(r.URL.Path, ".well-known") ||
			strings.Contains(r.URL.Path, "js.map") {
			return
		}
		id := fmt.Sprintf("%s_/%s", route, genRandID())
		ctx := &Ctx{
			id:           id,
			routeParams:  extractParams(route, r.URL.Path),
			cmp:          cmp,
			patchChan:    make(chan patch, 64),
			doneChan:     make(chan struct{}),
			signalValues: initSignalValues(cmp),
		}
		a.registerCtx(id, ctx)

		headElements := []h.H{}
		headElements = append(headElements, a.documentHeadIncludes...)
		initialSigs := map[string]any{"via_tab": id}
		for _, sig := range cmp.signals {
			if sm, ok := sig.(signalMeta); ok && !sm.hasError() {
				initialSigs[sm.displayID()] = sm.initialRawValue()
			}
		}
		initialSigsJSON, _ := json.Marshal(initialSigs)
		headElements = append(headElements,
			h.Meta(h.Data("signals", string(initialSigsJSON))),
			h.Meta(h.Data("init", "@get('/_sse')")),
			h.Meta(h.Data("init", fmt.Sprintf(`window.addEventListener('beforeunload', (evt) => {
			navigator.sendBeacon('/_sse/close', '%s');});`, id))),
		)

		bodyElements := []h.H{h.Div(h.ID(id), cmp.viewFn(ctx))}
		bodyElements = append(bodyElements, a.documentFootIncludes...)
		view := h.HTML5(h.HTML5Props{
			Title:     a.cfg.title,
			Head:      headElements,
			Body:      bodyElements,
			HTMLAttrs: a.documentHTMLAttrs,
		})
		_ = view.Render(w)
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

	sse := datastar.NewSSE(w, r, datastar.WithCompression(datastar.WithBrotli(datastar.WithBrotliLevel(5))))

	a.logDebug(ctx, "SSE connection established")

	ctx.mux.Lock()
	firstConnect := !ctx.initialized
	ctx.initialized = true
	ctx.mux.Unlock()

	if firstConnect {
		go func() {
			if ctx.cmp.initFn != nil {
				ctx.cmp.initFn(ctx)
			}
		}()
	}

	for {
		select {
		case <-sse.Context().Done():
			return
		case p, ok := <-ctx.patchChan:
			if !ok {
				return
			}
			switch p.typ {
			case patchTypeElements:
				sse.PatchElements(p.content)
			case patchTypeSignals:
				sse.PatchSignals([]byte(p.content))
			case patchTypeScript:
				sse.ExecuteScript(p.content)
			}
		}
	}
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

func (ctx *Ctx) injectSignals(sigs map[string]any) {
	if sigs == nil {
		return
	}
	for incomingID, val := range sigs {
		if incomingID == "via_tab" {
			continue
		}
		for sigID, sig := range ctx.cmp.signals {
			if sm, ok := sig.(signalMeta); ok && sm.displayID() == incomingID {
				ctx.signalValues[sigID] = &signalValue{raw: sm.coerce(val)}
				break
			}
		}
	}
}

func (ctx *Ctx) prepareSignalsForPatch() map[string]any {
	updatedSigs := make(map[string]any)
	for sigID, sig := range ctx.cmp.signals {
		if sm, ok := sig.(signalMeta); ok {
			if sv, exists := ctx.signalValues[sigID]; exists && sv.changed {
				updatedSigs[sm.displayID()] = sv.raw
				sv.changed = false
			}
		}
	}
	return updatedSigs
}

func initSignalValues(cmp *Cmp) map[string]*signalValue {
	vals := make(map[string]*signalValue, len(cmp.signals))
	for id, sig := range cmp.signals {
		if sm, ok := sig.(signalMeta); ok {
			vals[id] = &signalValue{raw: sm.initialRawValue()}
		}
	}
	return vals
}

func (a *App) handleSSEClose(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		log.Printf("Error reading body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	cID := string(body)
	ctx, err := a.getCtx(cID)
	if err != nil {
		a.logErr(ctx, "failed to handle session close: %v", err)
		return
	}
	a.disposeCtx(ctx)
	a.unregisterCtx(cID)
}

func extractParams(pattern, path string) map[string]string {
	p := strings.Split(strings.Trim(pattern, "/"), "/")
	u := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) != len(u) {
		return nil
	}
	params := make(map[string]string)
	for i := range p {
		if strings.HasPrefix(p[i], "{") && strings.HasSuffix(p[i], "}") {
			key := p[i][1 : len(p[i])-1]
			params[key] = u[i]
		} else if p[i] != u[i] {
			return nil
		}
	}
	return params
}
