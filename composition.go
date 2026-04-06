package via

import (
	"sync"

	"github.com/go-via/via/h"
)

// Cmp is the composition — created once per route at startup, shared by all requests.
type Cmp struct {
	app           *App
	route         string
	viewFn        func(ctx *Ctx) h.H
	actionFns     map[string]func(ctx *Ctx) error
	initFn        func(ctx *Ctx)
	disposeFn     func()
	components    []*Cmp
	appStateStore sync.Map
	signals       map[string]any
}

// View registers the render function for this composition.
func (c *Cmp) View(f func(ctx *Ctx) h.H) {
	if f == nil {
		panic("nil viewfn")
	}
	c.viewFn = f
}

// Action registers an event handler and returns a trigger for use in the view.
func (c *Cmp) Action(f func(ctx *Ctx) error) *actionTrigger {
	id := genRandID()
	if f == nil {
		return nil
	}
	c.actionFns[id] = f
	return &actionTrigger{id}
}

// Init registers a callback that runs once when the tab connects via SSE.
func (c *Cmp) Init(f func(ctx *Ctx)) {
	c.initFn = f
}

// Dispose registers a callback that runs when the session/tab ends.
func (c *Cmp) Dispose(f func()) {
	c.disposeFn = f
}

// Component registers a child composition and returns a render function for use in the view.
// Child actions and signals are merged into the parent so the runtime can find them.
func (c *Cmp) Component(initCmp func(cmp *Cmp)) func(ctx *Ctx) h.H {
	comp := &Cmp{
		app:       c.app,
		actionFns: c.actionFns,
		signals:   c.signals,
	}
	initCmp(comp)
	c.components = append(c.components, comp)
	compID := genRandID()
	return func(ctx *Ctx) h.H {
		return h.Div(h.ID("comp_"+compID), comp.viewFn(ctx))
	}
}
