package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func main() {
	v := via.New()

	v.Page("/", func(cmp *via.Cmp) {
		counterComp1 := cmp.Component(counterCompFn)
		counterComp2 := cmp.Component(counterCompFn)

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.H1(h.Text("Counter 1")),
				counterComp1(ctx),
				h.H1(h.Text("Counter 2")),
				counterComp2(ctx),
			)
		})
	})

	v.Start()
}

func counterCompFn(cmp *via.Cmp) {
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
}
