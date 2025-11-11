package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func main() {
	v := via.New()

	v.Page("/", func(c *via.Context) {
		greeting := c.Signal("Hello...")

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
				h.P(h.Span(h.Text("Greeting: ")), h.Span(greeting.Text())),
				h.Button(h.Text("Greet Bob"), greetBob.OnClick()),
				h.Button(h.Text("Greet Alice"), greetAlice.OnClick()),
			)
		})
	})

	v.Start()
}
