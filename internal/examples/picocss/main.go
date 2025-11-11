package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func main() {
	v := via.New()

	v.AppendToHead(h.Link(h.Rel("stylesheet"), h.Href("https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css")))

	v.Page("/", func(c *via.Context) {
		c.View(func() h.H {
			return h.Div(
				h.H1(h.Text("Hello PicoCSS!")),
				h.H2(h.Text("Hello PicoCSS!")),
				h.H3(h.Text("Hello PicoCSS!")),
				h.H4(h.Text("Hello PicoCSS!")),
				h.H5(h.Text("Hello PicoCSS!")),
				h.H6(h.Text("Hello PicoCSS!")),
				h.Div(h.Class("grid"),
					h.Button(h.Text("Primary")),
					h.Button(h.Class("secondary"), h.Text("Secondary")),
				),
			)
		})
	})
	v.Start()
}
