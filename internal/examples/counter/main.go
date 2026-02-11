package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func NewCounterPage() *via.V {
	v := via.New()
	v.Config(via.Options{ServerAddress: ":3000"})

	v.Page("/", func(c *via.Composition) {
		count := via.State(0)
		step := via.Signal(c, 1)

		increment := via.Action(c, func(s *via.Session) {
			count.Set(s, count.Get(s)+step.Get(s))
		})

		decrement := via.Action(c, func(s *via.Session) {
			count.Set(s, count.Get(s)-step.Get(s))
		})

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.H1(h.Text("Counter Example")),
				h.P(h.Textf("Count: %d", count.Get(s))),
				h.Label(h.Text("Step: ")),
				h.Input(h.Type("number"), h.Name("step"), step.Bind()),
				h.Div(
					h.Button(h.Text("-"), decrement.OnClick()),
					h.Button(h.Text("+"), increment.OnClick()),
				),
			)
		})
	})

	return v
}

func main() {
	v := NewCounterPage()
	v.Start()
}
