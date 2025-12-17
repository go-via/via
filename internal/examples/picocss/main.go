package main

import (
	"github.com/go-via/via"
	// "github.com/go-via/via-plugin-picocss/picocss"
	"github.com/go-via/via/h"
)

type Counter struct{ Count int }

func main() {
	v := via.New()

	v.Config(via.Options{
		DocumentTitle: "Via Counter",
		// Plugin is placed here. Use picocss.WithOptions(pococss.Options) to add the plugin
		// with a different color theme or to enable a classes for a wide range of colors.
		// Plugins: []via.Plugin{
		// 	picocss.Default,
		// },
	})

	v.Page("/", func(c *via.Context) {
		data := Counter{Count: 0}
		step := c.Signal(1)

		increment := c.Action(func() {
			data.Count += step.Int()
			c.Sync()
		})

		c.View(func() h.H {
			return h.Main(h.Class("container"), h.Br(),
				h.H1(h.Text("⚡ Via Counter")), h.Hr(),
				h.Div(
					h.H2(h.Textf("Count - %d", data.Count)),
					h.H5(h.Text("Step - "), step.Text()),
					h.Div(h.Role("group"),
						h.Input(h.Type("number"), step.Bind()),
						h.Button(h.Text("Increment"), increment.OnClick()),
					),
				),
			)
		})
	})

	v.Start()
}
