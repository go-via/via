package main

import (
	"github.com/go-via/via/internal/viaold"
	"github.com/go-via/via/internal/viaold/h"
)

func main() {
	v := via.New()

	v.Page("/", func(cmp *via.Cmp) {
		count := via.State(cmp, 0)
		step := via.Signal(cmp, 1)

		increment := cmp.Action(func(ctx *via.Ctx) error {
			count.Set(ctx, count.Get(ctx)+step.Get(ctx))
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.P(h.Textf("Count: %d", count.Get(ctx))),
				h.P(h.Span(h.Text("Step: ")), h.Span(step.Text())),
				h.Label(
					h.Text("Update Step: "),
					h.Input(h.Type("number"), step.Bind()),
				),
				h.Button(h.Text("Increment"), increment.OnClick()),
			)
		})
	})

	v.Start()
}
