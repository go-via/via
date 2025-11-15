package via

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"sync"

	"github.com/go-via/via/h"
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
	patchChan         chan patch
	actionRegistry    map[string]func()
	signals           *sync.Map
	mutex             sync.RWMutex
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
		c.parentPageCtx.signals.Store(sigID, sig)
	} else {
		c.signals.Store(sigID, sig)
	}
	return sig

}

func (c *Context) injectSignals(sigs map[string]any) {
	if sigs == nil {
		c.app.logErr(c, "signal injection failed: nil signals in ctx")
		return
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	for sigID, val := range sigs {
		if _, ok := c.signals.Load(sigID); !ok {
			c.signals.Store(sigID, &signal{
				id: sigID,
				t:  reflect.TypeOf(val),
				v:  reflect.ValueOf(val),
			})
			continue
		}
		item, _ := c.signals.Load(sigID)
		if sig, ok := item.(*signal); ok {
			sig.v = reflect.ValueOf(val)
			sig.changed = false
		}
	}
}

func (c *Context) getPatchChan() chan patch {
	// components use parent page sse stream
	var patchChan chan patch
	if c.isComponent() {
		patchChan = c.parentPageCtx.parentPageCtx.patchChan
	} else {
		patchChan = c.patchChan
	}
	return patchChan
}

func (c *Context) prepareSignalsForPatch() map[string]any {
	updatedSigs := make(map[string]any)
	c.signals.Range(func(sigID, value any) bool {
		if sig, ok := value.(*signal); ok {
			if sig.err != nil {
				c.app.logWarn(c, "signal '%s' is out of sync: %v", sig.id, sig.err)
				return true
			}
			if sig.changed {
				updatedSigs[sigID.(string)] = fmt.Sprintf("%v", sig.v)
			}
		}
		return true
	})
	return updatedSigs
}

// Sync pushes the current view state and signal changes to the browser immediately
// over the live SSE event stream.
func (c *Context) Sync() {
	patchChan := c.getPatchChan()
	elemsPatch := bytes.NewBuffer(make([]byte, 0))
	if err := c.view().Render(elemsPatch); err != nil {
		c.app.logErr(c, "sync view failed: %v", err)
		return
	}
	patchChan <- patch{patchTypeElements, elemsPatch.String()}

	c.mutex.RLock()
	defer c.mutex.RUnlock()
	updatedSigs := c.prepareSignalsForPatch()

	if len(updatedSigs) != 0 {
		outgoingSigs, _ := json.Marshal(updatedSigs)
		patchChan <- patch{patchTypeSignals, string(outgoingSigs)}
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
	patchChan := c.getPatchChan()
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
	patchChan <- patch{patchTypeElements, b.String()}
}

// SyncSignals pushes the current signal changes to the browser immediately
// over the live SSE event stream.
func (c *Context) SyncSignals() {
	patchChan := c.getPatchChan()
	c.mutex.RLock()
	updatedSigs := c.prepareSignalsForPatch()
	defer c.mutex.RUnlock()

	if len(updatedSigs) != 0 {
		outgoingSignals, _ := json.Marshal(updatedSigs)
		patchChan <- patch{patchTypeSignals, string(outgoingSignals)}
	}
}

func (c *Context) ExecScript(s string) {
	if s == "" {
		return
	}
	patchChan := c.getPatchChan()
	patchChan <- patch{patchTypeScript, s}
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
		signals:           new(sync.Map),
		patchChan:         make(chan patch, 100),
	}
}
