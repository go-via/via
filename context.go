package via

import (
	"bytes"
	"fmt"
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/go-via/via/h"
	"github.com/starfederation/datastar-go/datastar"
)

// Context is the living bridge between Go and the browser.
//
// It binds user state and actions, manages reactive signals, and defines UI through View.
type Context struct {
	id             string
	route          string
	app            *via
	view           func() h.H
	sse            *datastar.ServerSentEventGenerator
	actionRegistry map[string]func()
	signals        map[string]*signal
	signalsMux     sync.Mutex
	createdAt      time.Time
}

// View defines the UI rendered by this context.
// The function should return an h.H element (from via/h).
//
// Changes to signals or state can be pushed live with Sync().
func (c *Context) View(f func() h.H) {
	if f == nil {
		c.app.logErr(c, "failed to bind view to context: nil func")
	}
	c.view = func() h.H { return h.Div(h.ID(c.id), f()) }
}

type actionTrigger struct {
	id string
}

func (a *actionTrigger) OnClick() h.H {
	return h.Data("on:click", fmt.Sprintf("@get('/_action/%s')", a.id))
}

// Action registers a named event handler callable from the browser.
//
// Use h.OnClick("actionName") or similar event bindings to trigger actions.
// Signal updates from the browser are automatically injected in the context before the
// handler function executes.
func (c *Context) Action(f func()) *actionTrigger {
	// if id == "" {
	// 	c.app.logErr(c, "failed to bind action to context: id is ''")
	// }
	id := genRandID()
	if f == nil {
		c.app.logErr(c, "failed to bind action '%s' to context: nil func", id)
		return nil
	}
	c.actionRegistry[id] = f
	return &actionTrigger{id}
}

func (c *Context) getActionFn(id string) (func(), error) {
	if f, ok := c.actionRegistry[id]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("action '%s' not found", id)
}

func (c *Context) Signals() map[string]*signal {
	if c.signals == nil {
		c.app.logErr(c, "failed to get signal: nil signals in ctx")
		return make(map[string]*signal)
	}
	return c.signals
}

// Signal creates a reactive signal and initializes it with a value.
// Use Bind() to link the value of input elements to the signal and Text() to
// display the signal value and watch the UI update live as the input changes.
//
// Example:
//
//	h.Div(
//		h.P(h.Span(h.Text("Hello, ")), h.Span(mysignal.Text())),
//		h.Input(h.Value("World"), mysignal.Bind()),
//	)
//
// Signals are 'alive' only in the browser, but Via always injects their values into
// the Context before each action call.
// If any signal value is updated by the server the update is automatically sent to the
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
	c.signals[sigID] = sig
	return sig

}

func (c *Context) injectSignals(sigs map[string]any) {
	if sigs == nil {
		c.app.logErr(c, "signal injection failed: nil signals in ctx")
		return
	}
	for k, v := range sigs {
		if _, ok := c.signals[k]; !ok {
			continue
		}
		c.signals[k].v = reflect.ValueOf(v)
		c.signals[k].changed = false
	}
}

// Sync pushes the current view state and signal changes to the browser immediately
// over the live SSE connection.
func (c *Context) Sync() {
	if c.sse == nil {
		c.app.logErr(c, "sync view failed: no sse connection")
	}
	elemsPatch := bytes.NewBuffer(make([]byte, 0))
	if err := c.view().Render(elemsPatch); err != nil {
		c.app.logErr(c, "sync view failed: %v", err)
		return
	}
	_ = c.sse.PatchElements(elemsPatch.String())
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
		_ = c.sse.MarshalAndPatchSignals(updatedSigs)
	}
}

// SyncElements pushes an immediate html patch to the browser that merges DOM
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
// Then, the merge will only occur if the ID of the top level element mattches 'my-element'.
func (c *Context) SyncElements(elem h.H) {
	if c.sse == nil {
		c.app.logErr(c, "sync element failed: no sse connection")
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
	c.sse.PatchElements(b.String())
}

// SyncSignals pushes the current signal changes to the browser immediately
// over the live SSE connection.
func (c *Context) SyncSignals() {
	if c.sse == nil {
		c.app.logErr(c, "sync signals failed: sse connection not found")
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
		_ = c.sse.MarshalAndPatchSignals(updatedSigs)
	}
}

func newContext(id string, a *via) *Context {
	if a == nil {
		log.Fatalf("create context failed: app pointer is nil")
	}

	return &Context{
		id:             id,
		app:            a,
		actionRegistry: make(map[string]func()),
		signals:        make(map[string]*signal),
		createdAt:      time.Now(),
	}
}
