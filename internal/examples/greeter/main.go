package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"log"
)

func main() {
	v := via.New()

	v.Page("/", func(cmp *via.Cmp) {
		greeting := via.Signal(cmp, "Hello...")

		greetBob := cmp.Action(func(ctx *via.Ctx) error {
			greeting.SetValue(ctx, "Hello Bob!")
			return nil
		})

		greetAlice := cmp.Action(func(ctx *via.Ctx) error {
			greeting.SetValue(ctx, "Hello Alice!")
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
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
