package main

import (
	_ "embed"
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// In this example we create a Via application with a plugin that adds PicoCSS. The plugin
// is handed to Via in the app Configuration.

//go:embed pico.yellow.min.css
var picoCSSFile []byte

func main() {
	v := via.New()
	v.Config(via.Options{
		DocumentTitle: "Via With PicoCSS Plugin",
		Plugins:       []via.Plugin{PicoCSSPlugin},
	})

	v.Page("/", func(c *via.Context) {
		c.View(func() h.H {
			return h.Section(h.Class("container"),

				h.H1(h.Text("Hello from ⚡Via")),
				h.Div(h.Class("grid"),
					h.Button(h.Text("Primary")),
					h.Button(h.Class("secondary"), h.Text("Secondary")),
				),
			)
		})
	})
	v.Start(":3000")
}

func PicoCSSPlugin(v *via.V) {
	v.HandleFunc("GET /_plugins/picocss/assets/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write(picoCSSFile)
	})
	v.Config(via.Options{
		DocumentHeadIncludes: []h.H{
			h.Link(h.Rel("stylesheet"), h.Href("/_plugins/picocss/assets/style.css")),
		},
	})
}
