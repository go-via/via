package via

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"sync"

	"github.com/go-via/via/h"
)

// Context is the living bridge between Go and the browser.
//
// It holds runtime state, defines actions, manages reactive signals, and defines UI through View.
type Context struct {
	id                string
	route             string
	app               *App
	view              func() h.H
	routeParams       map[string]string
	componentRegistry map[string]*Context
	parentPageCtx     *Context
	patchChan         chan patch
	actionRegistry    map[string]func() error
	signals           *sync.Map
	mu                sync.RWMutex
}

// View defines the UI rendered by this context.
// The function should return an h.H element (from via/h).
//
// Changes to signals or state can be pushed live with Sync().
func (c *Context) View(f func() h.H) {
	if f == nil {
		panic("nil viewfn")
	}
	c.view = func() h.H { return h.Div(h.ID(c.id), f()) }
}

// Component registers a subcontext that has self contained data, actions and signals.
// It returns the component's view as a DOM node fn that can be placed in the view
// of the parent. Components can be added to components.
//
// Example:
//
//	counterCompFn := func(c *via.Context) {
//		(...)
//	}
//
//	v.Page("/", func(c *via.Context) {
//		counterComp := c.Component(counterCompFn)
//
//		c.View(func() h.H {
//			return h.Div(
//				h.H1(h.Text("Counter")),
//				counterComp(),
//			)
//		})
//	})
func (c *Context) Component(initCtx func(c *Context)) func() h.H {
	id := c.id + "/_component/" + genRandID()
	compCtx := newContext(id, c.route, c.app)
	if c.isComponent() {
		compCtx.parentPageCtx = c.parentPageCtx
	} else {
		compCtx.parentPageCtx = c
	}
	initCtx(compCtx)
	c.componentRegistry[id] = compCtx
	return compCtx.view
}

func (c *Context) isComponent() bool {
	return c.parentPageCtx != nil
}

// Action registers an event handler and returns a trigger to that event that
// that can be added to the view fn as any other via.h element.
//
// Example:
//
//	n := 0
//	increment := c.Action(func(){
//		 n++
//		 c.Sync()
//	})
//
//	c.View(func() h.H {
//		 return h.Div(
//		 	 	h.P(h.Textf("Value of n: %d", n)),
//		 	 	h.Button(h.Text("Increment n"), increment.OnClick()),
//		 )
//	})
func (c *Context) Action(f func() error) *actionTrigger {
	id := genRandID()
	if f == nil {
		c.app.logErr(c, "failed to bind action '%s' to context: nil func", id)
		return nil
	}

	if c.isComponent() {
		c.parentPageCtx.actionRegistry[id] = f
	} else {
		c.actionRegistry[id] = f
	}
	return &actionTrigger{id}
}

func (c *Context) getActionFn(id string) (func() error, error) {
	if f, ok := c.actionRegistry[id]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("action '%s' not found", id)
}

func (c *Context) injectSignals(sigs map[string]any) {
	if sigs == nil {
		c.app.logErr(c, "signal injection failed: nil signals")
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for sigID, val := range sigs {
		// Skip via-ctx
		if sigID == "via-ctx" {
			continue
		}
		// Fast path: lookup by map key (== signal id when no tag)
		if item, ok := c.signals.Load(sigID); ok {
			if entry, ok := item.(signalEntry); ok {
				entry.setRawValue(val)
			}
			continue
		}
		// Slow path: find by displayID (for tagged signals)
		var found signalEntry
		c.signals.Range(func(_, value any) bool {
			if entry, ok := value.(signalEntry); ok {
				if entry.displayID() == sigID {
					found = entry
					return false
				}
			}
			return true
		})
		if found != nil {
			found.setRawValue(val)
		} else {
			c.signals.Store(sigID, &signalOf[any]{id: sigID, val: val})
		}
	}
}

func (c *Context) getPatchChan() chan patch {
	// components use parent page sse stream
	var patchChan chan patch
	if c.isComponent() {
		patchChan = c.parentPageCtx.patchChan
	} else {
		patchChan = c.patchChan
	}
	return patchChan
}

func (c *Context) prepareSignalsForPatch() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	updatedSigs := make(map[string]any)
	c.signals.Range(func(_, value any) bool {
		entry, ok := value.(signalEntry)
		if !ok {
			return true
		}
		if entry.hasError() {
			c.app.logWarn(c, "signal '%s' is out of sync: %v", entry.getID(), entry.getErr())
			return true
		}
		if entry.isChanged() {
			updatedSigs[entry.displayID()] = entry.rawValue()
		}
		return true
	})
	return updatedSigs
}

// sendPatch queues a patch on this *Context sse stream. If the sse is closed or queue is full, the patch
// is dropped to prevent runtime blocks.
func (c *Context) sendPatch(p patch) {
	patchChan := c.getPatchChan()
	select {
	case patchChan <- p:
	default: // closed or buffer full - drop patch without blocking
	}
}

// Sync pushes the current view state and signal changes to the browser immediately
// over the live SSE event stream.
func (c *Context) Sync() {
	elemsPatch := bytes.NewBuffer(make([]byte, 0))
	if err := c.view().Render(elemsPatch); err != nil {
		c.app.logErr(c, "sync view failed: %v", err)
		return
	}
	c.sendPatch(patch{patchTypeElements, elemsPatch.String()})

	updatedSigs := c.prepareSignalsForPatch()

	if len(updatedSigs) != 0 {
		outgoingSigs, _ := json.Marshal(updatedSigs)
		c.sendPatch(patch{patchTypeSignals, string(outgoingSigs)})
	}
}

// autoSync is called automatically after each action. It calls Sync() so that
// view and signal state are pushed to the browser without requiring an explicit c.Sync() call.
func (c *Context) autoSync() {
	c.Sync()
}

// SyncElements pushes an immediate html patch over the live SSE stream to the
// browser that merges with the DOM
//
// For the merge to occur, each top lever element in the patch needs to have
// an ID that matches the ID of an element that already sits in the view.
//
// Example:
//
// If the view already contains the element:
//
//	h.Div(
//		h.ID("my-element"),
//		h.P(h.Text("Hello from Via!"))
//	)
//
// Then, the merge will only occur if the ID of one of the top level elements in the patch
// matches 'my-element'.
func (c *Context) SyncElements(elem ...h.H) {
	b := bytes.NewBuffer(nil)
	for idx, el := range elem {
		if el == nil {
			c.app.logWarn(c, "sync elements failed: element at idx=%d is nil", idx)
			continue
		}
		if err := el.Render(b); err != nil {
			c.app.logWarn(c, "sync elements failed: element at idx=%d has invalid html", idx)
			continue
		}
	}
	c.sendPatch(patch{patchTypeElements, b.String()})
}

// SyncSignals pushes the current signal changes to the browser immediately
// over the live SSE event stream.
func (c *Context) SyncSignals() {
	updatedSigs := c.prepareSignalsForPatch()
	if len(updatedSigs) != 0 {
		outgoingSignals, _ := json.Marshal(updatedSigs)
		c.sendPatch(patch{patchTypeSignals, string(outgoingSignals)})
	}
}

func (c *Context) ExecScript(s string) {
	if s == "" {
		c.app.logWarn(c, "exec script failed: empty script")
		return
	}
	c.sendPatch(patch{patchTypeScript, s})
}

func (c *Context) injectRouteParams(params map[string]string) {
	if params == nil {
		return
	}
	m := make(map[string]string)
	c.mu.Lock()
	defer c.mu.Unlock()
	maps.Copy(m, params)
	c.routeParams = m

}

// GetPathParam retrieves the value from the page request URL for the given parameter name
// or an empty string if not found.
//
// Example:
//
//	v.Page("/users/{user_id}", func(c *via.Context) {
//
//			userID := GetPathParam("user_id")
//
//			c.View(func() h.H {
//					return h.Div(
//							h.H1(h.Textf("User ID: %s", userID)),
//					)
//			})
//	})
func (c *Context) GetPathParam(param string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if p, ok := c.routeParams[param]; ok {
		return p
	}
	return ""
}

func newContext(id string, route string, a *App) *Context {
	if a == nil {
		log.Fatal("create context failed: app pointer is nil")
	}

	return &Context{
		id:                id,
		route:             route,
		routeParams:       make(map[string]string),
		app:               a,
		componentRegistry: make(map[string]*Context),
		actionRegistry:    make(map[string]func() error),
		signals:           new(sync.Map),
		patchChan:         make(chan patch, 64),
	}
}
