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
	"github.com/go-via/via/plugins/picocss"
)

type Counter struct {
	Hits via.StateTab[int]
	Step via.Signal[int] `via:"step,init=1"`
}

// Action methods drop the error return when nothing in the body can
// fail meaningfully — Update / Set don't surface errors.

func (c *Counter) Inc(ctx *via.Ctx) {
	c.Hits.Update(ctx, func(n int) int { return n + c.Step.Get(ctx) })
}

func (c *Counter) Reset(ctx *via.Ctx) {
	c.Hits.Set(ctx, 0)
	c.Step.Set(ctx, 1)
}

func (c *Counter) View(ctx *via.CtxR) h.H {
	return h.Main(h.Class("container"),
		h.Article(
			h.H1(h.Text("Counter")),
			h.P(h.Text("Step: "), c.Step.Text()),
			h.P(h.Text("Count: "), h.Textf("%d", c.Hits.Get(ctx))),
			h.Input(h.Type("number"), h.Min("1"), c.Step.Bind()),
			h.Div(
				h.Style("display:flex;gap:0.5rem"),
				h.Button(h.Style("margin:0"), h.Text("+"), on.Click(c.Inc)),
				h.Button(h.Style("margin:0"), h.Class("secondary"), h.Text("Reset"), on.Click(c.Reset)),
			),
		),
	)
}

func main() {
	app := via.New(
		via.WithTitle("Counter"),
		via.WithPlugins(picocss.Plugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeAmber}))),
	)
	via.Mount[Counter](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
