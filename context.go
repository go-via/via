package via

import (
	"bytes"
	"fmt"
	"log"
	"reflect"
	"sync"

	"github.com/go-via/via/h"
	"github.com/starfederation/datastar-go/datastar"
)

// Context is the living bridge between Go and the browser.
//
// It holds runtime state, defines actions, manages reactive signals, and defines UI through View.
type Context struct {
	id                string
	route             string
	app               *V
	view              func() h.H
	componentRegistry map[string]*Context
	parentPageCtx     *Context
	sse               *datastar.ServerSentEventGenerator
	actionRegistry    map[string]func()
	signals           map[string]*signal
	signalsMux        sync.Mutex
}

// View defines the UI rendered by this context.
// The function should return an h.H element (from via/h).
//
// Changes to signals or state can be pushed live with Sync().
func (c *Context) View(f func() h.H) {
	if f == nil {
		c.app.logErr(c, "failed to bind view to context: nil func")
		return
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
func (c *Context) Component(f func(c *Context)) func() h.H {
	id := c.id + "/_component/" + genRandID()
	compCtx := newContext(id, c.route, c.app)
	if c.isComponent() {
		compCtx.parentPageCtx = c.parentPageCtx
	} else {
		compCtx.parentPageCtx = c
	}
	f(compCtx)
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
func (c *Context) Action(f func()) *actionTrigger {
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

func (c *Context) getActionFn(id string) (func(), error) {
	if f, ok := c.actionRegistry[id]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("action '%s' not found", id)
}

// Signal creates a reactive signal and initializes it with the given value.
// Use Bind() to link the value of input elements to the signal and Text() to
// display the signal value and watch the UI update live as the input changes.
//
// Example:
//
//	mysignal := c.Signal("world")
//
//	c.View(func() h.H {
//		return h.Div(
//			h.P(h.Span(h.Text("Hello, ")), h.Span(mysignal.Text())),
//			h.Input(mysignal.Bind()),
//		)
//	})
//
// Signals are 'alive' only in the browser, but Via always injects their values into
// the Context before each action call.
// If any signal value is updated by the server, the update is automatically sent to the
// browser when using Sync() or SyncSignsls().
func (c *Context) Signal(v any) *signal {
	sigID := genRandID()
	if v == nil {
		c.app.logErr(c, "failed to bind signal: nil signal value")
		dummy := "Error"
		return &signal{
			id:  sigID,
			v:   reflect.ValueOf(dummy),
			t:   reflect.TypeOf(dummy),
			err: fmt.Errorf("context '%s' failed to bind signal '%s': nil signal value", c.id, sigID),
		}
	}
	sig := &signal{
		id:      sigID,
		v:       reflect.ValueOf(v),
		t:       reflect.TypeOf(v),
		changed: true,
	}

	// components register signals on parent page
	if c.isComponent() {
		c.parentPageCtx.signals[sigID] = sig
	} else {
		c.signals[sigID] = sig
	}
	return sig

}

func (c *Context) injectSignals(sigs map[string]any) {
	if sigs == nil {
		c.app.logErr(c, "signal injection failed: nil signals in ctx")
		return
	}
	for k, v := range sigs {
		if _, ok := c.signals[k]; !ok {
			c.signals[k] = &signal{
				id: k,
				t:  reflect.TypeOf(v),
				v:  reflect.ValueOf(v),
			}
			continue
		}
		c.signals[k].v = reflect.ValueOf(v)
		c.signals[k].changed = false
	}
}

func (c *Context) getSSE() *datastar.ServerSentEventGenerator {
	// components use parent page sse stream
	var sse *datastar.ServerSentEventGenerator
	if c.isComponent() {
		sse = c.parentPageCtx.sse
	} else {
		sse = c.sse
	}
	return sse
}

// Connected checks if there's an SSE connection.
func (c *Context) Connected() bool {
	return c.getSSE() != nil
}

// Sync pushes the current view state and signal changes to the browser immediately
// over the live SSE event stream.
func (c *Context) Sync() {
	sse := c.getSSE()
	if sse == nil {
		c.app.logWarn(c, "view out of sync: no sse stream")
		return
	}
	elemsPatch := bytes.NewBuffer(make([]byte, 0))
	if err := c.view().Render(elemsPatch); err != nil {
		c.app.logErr(c, "sync view failed: %v", err)
		return
	}
	_ = sse.PatchElements(elemsPatch.String())
	updatedSigs := make(map[string]any)
	for id, sig := range c.signals {
		if sig.err != nil {
			c.app.logWarn(c, "failed to sync signal '%s': %v", sig.id, sig.err)
		}
		if sig.changed && sig.err == nil {
			updatedSigs[id] = fmt.Sprintf("%v", sig.v)
		}
	}
	if len(updatedSigs) != 0 {
		_ = sse.MarshalAndPatchSignals(updatedSigs)
	}
}

// SyncElements pushes an immediate html patch over the live SSE stream to the
// browser that merges with the DOM
//
// For the merge to occur, the top level element in the patch needs to have
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
// Then, the merge will only occur if the ID of the top level element in the patch
// matches 'my-element'.
func (c *Context) SyncElements(elem h.H) {
	sse := c.getSSE()
	if sse == nil {
		c.app.logWarn(c, "elements out of sync: no sse stream")
		return
	}
	if c.view == nil {
		c.app.logErr(c, "sync element failed: viewfn is nil")
		return
	}
	if elem == nil {
		c.app.logErr(c, "sync element failed: view func is nil")
		return
	}
	b := bytes.NewBuffer(make([]byte, 0))
	_ = elem.Render(b)
	_ = sse.PatchElements(b.String())
}

// SyncSignals pushes the current signal changes to the browser immediately
// over the live SSE event stream.
func (c *Context) SyncSignals() {
	sse := c.getSSE()
	if sse == nil {
		c.app.logWarn(c, "signals out of sync: no sse stream")
		return
	}
	updatedSigs := make(map[string]any)
	for id, sig := range c.signals {
		if sig.err != nil {
			c.app.logWarn(c, "signal out of sync'%s': %v", sig.id, sig.err)
		}
		if sig.changed && sig.err == nil {
			updatedSigs[id] = fmt.Sprintf("%v", sig.v)
		}
	}
	if len(updatedSigs) != 0 {
		_ = sse.MarshalAndPatchSignals(updatedSigs)
	}
}

func (c *Context) ExecScript(s string) {
	sse := c.getSSE()
	if sse == nil {
		c.app.logWarn(c, "script out of sync: no sse stream")
		return
	}
	_ = sse.ExecuteScript(s)
}

func newContext(id string, route string, app *V) *Context {
	if app == nil {
		log.Fatalf("create context failed: app pointer is nil")
	}

	return &Context{
		id:                id,
		route:             route,
		app:               app,
		componentRegistry: make(map[string]*Context),
		actionRegistry:    make(map[string]func()),
		signals:           make(map[string]*signal),
	}
}
