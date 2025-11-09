package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type Counter struct{ Count int }

func main() {
	v := via.New()

	v.Config(via.Options{
		DocumentTitle: "Live Reload",
		Plugins:       []via.Plugin{LiveReloadPlugin},
	})

	v.Page("/", func(c *via.Context) {
		data := Counter{Count: 0}
		step := c.Signal(1)

		increment := c.Action(func() {
			data.Count += step.Int()
			c.Sync()
		})

		c.View(func() h.H {
			return h.Div(
				h.H1(h.Text("Live Reload")),
				h.P(h.Textf("Count: %d", data.Count)),
				h.P(h.Span(h.Text("Step: ")), h.Span(step.Text())),
				h.Label(
					h.Text("Update Step: "),
					h.Input(h.Type("number"), step.Bind()),
				),
				h.Button(h.Text("Increment"), increment.OnClick()),
			)
		})
	})

	v.Start(":3000")
}
