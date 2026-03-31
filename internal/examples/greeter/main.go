package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"log"
)

func main() {
	v := via.New()

	v.Page("/", func(c *via.Context) {
		greeting := via.Signal(c, "Hello...")

		greetBob := c.Action(func() error {
			greeting.SetValue("Hello Bob!")
			c.SyncSignals()
			return nil
		})

		greetAlice := c.Action(func() error {
			greeting.SetValue("Hello Alice!")
			c.SyncSignals()
			return nil
		})

		c.View(func() h.H {
			return h.Div(
				h.P(h.Span(h.Text("Greeting: ")), h.Span(greeting.Text())),
				h.Button(h.Text("Greet Bob"), greetBob.OnClick()),
				h.Button(h.Text("Greet Alice"), greetAlice.OnClick()),
			)
		})
	})

	if err := v.Start(); err != nil {
		log.Fatal(err)
	}
}
