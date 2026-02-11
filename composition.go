package via

import (
	"github.com/go-via/via/h"
)

type Composition struct {
	id      string
	route   string
	viewFn  func(*Session) h.H
	actions map[string]func(*Session)
	signals []signalRegistration
}

type signalRegistration struct {
	id      string
	initial any
}

func (c *Composition) ID() string {
	return c.id
}

func (c *Composition) View(viewFn func(s *Session) h.H) {
	if viewFn == nil {
		panic("page composition contains no view")
	}
	c.viewFn = func(s *Session) h.H {
		return h.Main(h.ID(c.id), viewFn(s))
	}
}
