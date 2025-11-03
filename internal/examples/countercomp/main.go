package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func main() {
	v := via.New()

	v.Page("/", func(c *via.Context) {
		counterComp1 := c.Component(counterCompFn)
		counterComp2 := c.Component(counterCompFn)

		c.View(func() h.H {
			return h.Div(
				h.H1(h.Text("Counter 1")),
				counterComp1(),
				h.H1(h.Text("Counter 2")),
				counterComp2(),
			)
		})
	})

	v.Start(":3000")
}

func counterCompFn(c *via.Context) {
	count := 0
	step := c.Signal(1)

	increment := c.Action(func() {
		count += step.Int()
		c.Sync()
	})

	c.View(func() h.H {
		return h.Div(
			h.P(h.Textf("Count: %d", count)),
			h.P(h.Span(h.Text("Step: ")), h.Span(step.Text())),
			h.Label(
				h.Text("Update Step: "),
				h.Input(h.Type("number"), step.Bind()),
			),
			h.Button(h.Text("Increment"), increment.OnClick()),
		)
	})
}
