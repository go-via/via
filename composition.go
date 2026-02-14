package via

import (
	"github.com/go-via/via/h"
)

type compOwner struct {
	id     string
	viewFn func(*Context) h.H
}

type Composition struct {
	id           string
	route        string
	viewFn       func(ctx *Context) h.H
	actions      map[string]func(*Context)
	actionOwners map[string]compOwner
	signals      []signalRegistration
	states       []stateRegistration
	isComponent  bool
	viewCalled   bool
}

type signalRegistration struct {
	id      string
	initial any
}

type stateRegistration struct {
	id      string
	initial any
	scope   Scope
}

func (c *Composition) ID() string {
	return c.id
}

func (c *Composition) TestActions() map[string]func(*Context) {
	return c.actions
}

func (c *Composition) TestActionOwners() map[string]compOwner {
	return c.actionOwners
}

func (c *Composition) View(viewFn func(ctx *Context) h.H) {
	if viewFn == nil {
		panic("page composition contains no view")
	}
	c.viewCalled = true
	if c.isComponent {
		c.viewFn = viewFn
	} else {
		c.viewFn = func(ctx *Context) h.H {
			return h.Main(h.ID(c.id), viewFn(ctx))
		}
	}
}
