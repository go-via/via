package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// NewNestedComponentApp creates an app with nested components for testing.
func main() {
	v := via.New()
	// Inner component - simple counter
	makeInnerCounter := func(label string) via.ComposeFn {
		return func(c *via.Composition) {
			count := via.State(c, 0)
			increment := via.Action(c, func(s *via.Session) {
				count.Set(s, count.Get(s)+1)
			})
			c.View(func(s *via.Session) h.H {
				return h.Div(
					h.P(h.Textf("%s: %d", label, count.Get(s))),
					h.Button(h.Text("+"), increment.OnClick()),
				)
			})
		}
	}

	// Outer component - contains two inner counters
	makePanel := via.ComposeFn(func(c *via.Composition) {
		counterA := c.Component(makeInnerCounter("Counter A"))
		counterB := c.Component(makeInnerCounter("Counter B"))

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.H2(h.Text("Panel")),
				counterA.Mount(s),
				counterB.Mount(s),
			)
		})
	})

	v.Page("/", func(c *via.Composition) {
		panel := c.Component(makePanel)

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.H1(h.Text("Nested Components")),
				panel.Mount(s),
			)
		})
	})
	v.Start()
}

