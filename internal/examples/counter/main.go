// Counter demo for the typed-API surface.
//
//	go run ./internal/examples/counter
//	open http://localhost:3000
package main

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type Counter struct {
	Hits via.State[int]
	Step via.Signal[int] `via:"step,init=1"`
}

// Action methods drop the error return when nothing in the body can
// fail meaningfully — via.Add / Set don't surface errors.

func (c *Counter) Inc(ctx *via.Ctx) {
	via.Add(ctx, &c.Hits, c.Step.Get(ctx))
}

func (c *Counter) Reset(ctx *via.Ctx) {
	c.Hits.Set(ctx, 0)
	c.Step.Set(ctx, 1)
}

func (c *Counter) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.H1(h.Text("Counter")),
		h.P(h.Text("Step: "), c.Step.Text()),
		h.P(h.Text("Count: "), c.Hits.Text()),
		h.Input(h.Type("number"), h.Min("1"), c.Step.Bind()),
		h.Button(h.Text("+"), on.Click(c.Inc)),
		h.Button(h.Text("Reset"), on.Click(c.Reset)),
	)
}

func main() {
	app := via.New(via.WithTitle("Counter"))
	via.Mount[Counter](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
