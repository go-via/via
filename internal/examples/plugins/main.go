package main

import (
	_ "embed"
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Example of a Via application with a plugin that adds PicoCSS. The plugin
// is handed to Via in the Configuration.

//go:embed pico.yellow.min.css
var picoCSSFile []byte

func main() {
	v := via.New(
		via.WithTitle("Via With PicoCSS Plugin"),
		via.WithPlugins(picoCSSPlugin{}),
	)

	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Section(h.Class("container"),

				h.H1(h.Text("Hello from ⚡Via")),
				h.Div(h.Class("grid"),
					h.Button(h.Text("Primary")),
					h.Button(h.Class("secondary"), h.Text("Secondary")),
				),
			)
		})
	})
	v.Start()
}

type picoCSSPlugin struct{}

func (picoCSSPlugin) Register(v *via.App) {
	v.HandleFunc("GET /_plugins/picocss/assets/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write(picoCSSFile)
	})
	v.AppendToHead(h.Link(h.Rel("stylesheet"), h.Href("/_plugins/picocss/assets/style.css")))
}
