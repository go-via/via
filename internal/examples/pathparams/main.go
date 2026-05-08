// Pathparams demonstrates path:"name" tag-driven decoding into typed fields.
//
//	go run ./internal/examples/pathparams
//	open http://localhost:3000/counters/foo/5
package main

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/picocss"
)

type CounterPage struct {
	CounterID   string `path:"counter_id"`
	StartAtStep int    `path:"start_at_step"`

	Count via.State[int]
	Step  via.Signal[int] `via:"step,init=1"`
}

func (c *CounterPage) Init(ctx *via.Ctx) error {
	if c.StartAtStep > 0 {
		c.Step.Set(ctx, c.StartAtStep)
	}
	return nil
}

func (c *CounterPage) Increment(ctx *via.Ctx) error {
	via.Add(ctx, &c.Count, c.Step.Get(ctx))
	return nil
}

func (c *CounterPage) View(ctx *via.Ctx) h.H {
	return h.Main(h.Class("container"),
		h.H3(h.Text(c.CounterID)),
		h.Hr(),
		h.H5(h.Textf("Count %d", c.Count.Get(ctx))),
		h.P(h.Text("Step: "), c.Step.Text()),
		h.FieldSet(h.Role("group"),
			h.Input(h.Type("number"), c.Step.Bind()),
			h.Button(h.Text("Increment"), on.Click(c.Increment)),
		),
	)
}

func main() {
	app := via.New(
		via.WithTitle("Path Params"),
		via.WithPlugins(picocss.Plugin()),
	)
	via.Mount[CounterPage](app, "/counters/{counter_id}/{start_at_step}")
	_ = http.ListenAndServe(":3000", app)
}
