package main

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
)

type Feature struct {
	Name        string
	Description string
}

var allFeatures = []Feature{
	{"Zero JavaScript", "Write pure Go. Via handles all reactivity server-side."},
	{"Type Safe", "Generics provide compile-time safety for state and signals."},
	{"Real-time Updates", "Server-Sent Events stream state changes instantly."},
	{"Components", "Reusable UI components with encapsulated state."},
	{"Actions", "Type-safe event handlers with full Go tooling."},
	{"Signals", "Client-side reactive values for browser state."},
}

func main() {
	v := via.New()

	v.Config(via.Options{
		DocumentTitle: "Via + Pico CSS",
		Plugins: []via.Plugin{
			picocss.New(
				picocss.WithThemes(picocss.AllPicoThemes),
				picocss.WithDefaultTheme(picocss.PicoThemeAmber),
				picocss.WithColorClasses(),
			),
		},
	})

	v.Page("/", func(c *via.Context) {
		count := c.Signal(len(allFeatures))

		increment := c.Action(func() {
			if count.Int() < len(allFeatures) {
				count.SetValue(count.Int() + 1)
				c.Sync()
			}
		})

		decrement := c.Action(func() {
			if count.Int() > 1 {
				count.SetValue(count.Int() - 1)
				c.Sync()
			}
		})

		c.View(func() h.H {
			visible := allFeatures[:count.Int()]

			items := make([]h.H, len(visible))
			for i, f := range visible {
				items[i] = h.Li(h.Strong(h.Text(f.Name+": ")), h.Text(f.Description))
			}

			themeButtons := make([]h.H, len(picocss.AllPicoThemes))
			for i, t := range picocss.AllPicoThemes {
				themeButtons[i] = h.Button(
					h.Class("outline pico-color-"+t.String()),
					h.Data("on:click", "$_picoTheme='"+t.String()+"'"),
					h.Text(t.String()),
				)
			}

			return h.Body(
				h.Nav(h.Class("container-fluid"),
					h.Ul(h.Li(h.Strong(h.Text("⚡ Via + Pico CSS")))),
					h.Ul(
						h.Li(h.Button(
							h.Class("outline secondary"),
							h.Data("on:click", "$_picoDarkMode=!$_picoDarkMode"),
							h.Text("Toggle dark mode"),
						)),
					),
				),
				h.Main(h.Class("container"),
					h.Article(
						h.H2(h.Text("Theme")),
						h.Div(themeButtons...),
					),
					h.Article(
						h.H2(h.Text("Features")),
						h.Div(h.Role("group"),
							h.Button(h.Text("-"), decrement.OnClick()),
							h.Button(h.Text("+"), increment.OnClick()),
						),
						h.Ul(items...),
					),
				),
			)
		})
	})

	v.Start()
}
