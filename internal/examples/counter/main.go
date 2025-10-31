package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type CounterState struct{ Count int }

func main() {
	v := via.New()

	v.Page("/", func(c *via.Context) {

		s := CounterState{Count: 0}

		step := c.Signal(1)
		greeting := c.Signal("Hello...")

		increment := c.Action(func() {
			s.Count += step.Int()
			c.Sync()
		})
		greetBob := c.Action(func() {
			greeting.SetValue("Hello Bob!")
			c.SyncSignals()
		})
		greetAlice := c.Action(func() {
			greeting.SetValue("Hello Alice!")
			c.SyncSignals()
		})

		c.View(func() h.H {
			return h.Div(
				h.P(h.Textf("Count: %d", s.Count)),
				h.P(h.Span(h.Text("Step: ")), h.Span(step.Text())),
				h.Label(
					h.Text("Update Step: "),
					h.Input(h.Type("number"), step.Bind()),
				),
				h.Button(h.Text("Increment"), increment.OnClick()),

				h.P(h.Span(h.Text("Greeting: ")), h.Span(greeting.Text())),
				h.Button(h.Text("Greet Alice"), greetBob.OnClick()),
				h.Button(h.Text("Greet Alice"), greetAlice.OnClick()),
			)
		})
	})

	v.Start(":3000")
}

//
// c.View(func() h.H {
// 	return Layout(
// 		h.Div(
// 			h.Meta(h.Data("init", "@get('/_sse')")),
// 			h.P(h.Data("text", "$via-ctx")),
// 			h.Div(
// 				counter(),
// 				h.Data("signals:step", "1"),
// 				h.Label(h.Text("Step")),
// 				h.Input(h.Data("bind", "step")),
// 				h.Button(
// 					h.Text("Trigger foo"),
// 					h.Data("on:click", "@get('/_action/foo')"),
// 				),
// 			),
// 		),
// 	)
// })

// conterComponent := c.Component("counter1", CounterComponent)
//
// in c.View of page add CounterComponent
//
// func CounterComponent(c *via.Context){
// 	s := CounterState{ Count: 1 }
// 	step := c.Signal(1)
//
// 	c.View(func() h.H {
// 		return h.Div(
// 			h.P(h.Textf("Count: %d", s.Count)),
// 			h.Label(
// 				h.Text("Step"),
// 				h.Input(h.Type("number"), step.Bind()),
// 			),
// 			h.Button(h.Text("Increment"), h.OnClick("inc")),
// 		)
// 	})
//
// 	c.Action("inc", func() {
// 		s.Count += step
// 		c.Sync()
// 	})
// }
