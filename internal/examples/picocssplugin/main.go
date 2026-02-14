package main

import (
	"log"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
)

var themeColors = map[string]string{
	"amber":   "#f59f00",
	"blue":    "#2563eb",
	"cyan":    "#0891b2",
	"fuchsia": "#c026d3",
	"green":   "#16a34a",
	"grey":    "#6b7280",
	"indigo":  "#4f46e5",
	"jade":    "#0d9488",
	"lime":    "#65a30d",
	"orange":  "#ea580c",
	"pink":    "#db2777",
	"pumpkin": "#ea580c",
	"purple":  "#9333ea",
	"red":     "#dc2626",
	"sand":    "#c2b280",
	"slate":   "#475569",
	"violet":  "#7c3aed",
	"yellow":  "#ca8a04",
	"zinc":    "#52525b",
}

func main() {
	v := NewPicoCSSPluginPage()
	log.Println("PicoCSSPlugin example: http://localhost:3000")
	v.Start()
}

func NewPicoCSSPluginPage() *via.V {
	v := via.New()

	plugin := picocss.New(picocss.Options{
		Themes:       picocss.AllThemes,
		DefaultTheme: "blue",
	})
	plugin.Register(v)

	v.Page("/", func(c *via.Composition) {
		theme := picocss.Theme(c, picocss.Options{
			Themes:       picocss.AllThemes,
			DefaultTheme: "blue",
		})

		features := via.State(c, 0)

		increment := via.Action(c, func(s *via.Session) {
			features.Set(s, features.Get(s)+1)
		})

		decrement := via.Action(c, func(s *via.Session) {
			if features.Get(s) > 0 {
				features.Set(s, features.Get(s)-1)
			}
		})

		themeButtons := func(s *via.Session) []h.H {
			var buttons []h.H
			for _, themeName := range picocss.AllThemes {
				color := themeColors[themeName]
				buttons = append(buttons,
					h.Button(
						h.Text(themeName),
						h.Data("on:click", "$_picoTheme = '"+themeName+"'"),
						h.Attr("style", "background-color: "+color+"; border-color: "+color+"; color: white;"),
					),
				)
			}
			return buttons
		}

		c.View(func(s *via.Session) h.H {
			gridChildren := []h.H{h.Class("grid")}
			gridChildren = append(gridChildren, themeButtons(s)...)

			return h.Body(
				h.Header(
					h.Nav(
						h.A(h.Href("#"), h.Text("Via")),
						h.Ul(
							h.Li(h.A(h.Href("#features"), h.Text("Features"))),
							h.Li(h.A(h.Href("#docs"), h.Text("Docs"))),
							h.Li(h.A(h.Href("#github"), h.Text("GitHub"))),
						),
					),
				),

				h.Main(
					h.Div(
						h.Class("container"),
						h.Article(
							h.H1(h.Text("Build Reactive Web Apps with Go")),
							h.P(h.Textf("Via eliminates JavaScript through Server-Sent Events and Datastar. Write Go, get real-time web apps.")),
							h.P(
								h.A(h.Class("btn primary"), h.Href("#get-started"), h.Text("Get Started")),
								h.A(h.Class("btn secondary"), h.Href("#demo"), h.Text("See Demo")),
							),
						),

						h.H2(h.ID("features"), h.Text("Features")),
						h.Div(
							h.Class("grid"),
							h.Article(
								h.H3(h.Text("Zero JavaScript")),
								h.P(h.Text("Write pure Go. Via handles all reactivity server-side.")),
							),
							h.Article(
								h.H3(h.Text("Real-time Updates")),
								h.P(h.Text("Server-Sent Events stream state changes instantly.")),
							),
							h.Article(
								h.H3(h.Text("Type Safe")),
								h.P(h.Text("Generics provide compile-time safety for state and signals.")),
							),
						),

						h.H2(h.ID("demo"), h.Text("Interactive Demo")),
						h.Article(
							h.H3(h.Text("Feature Counter")),
							h.P(h.Text("Click to see real-time reactivity in action.")),
							h.Div(
								h.Class("grid"),
								h.Button(h.Text("-"), h.Data("on:click", "@get('/_action/"+decrement.ID()+"')")),
								h.FigCaption(h.Textf("Features: %d", features.Get(s))),
								h.Button(h.Text("+"), h.Data("on:click", "@get('/_action/"+increment.ID()+"')")),
							),
						),

						h.H2(h.ID("theme-switcher"), h.Text("Theme Switcher")),
						h.P(h.Text("Click a color to switch themes:")),
						h.Div(
							h.Class("theme-grid"),
							h.Attr("style", "display: flex; flex-wrap: wrap; gap: 0.5rem; justify-content: center;"),
							h.Div(gridChildren...),
						),
					),
				),

				h.Footer(
					h.Class("container"),
					h.P(
						h.Text("Built with "),
						h.A(h.Href("https://via"), h.Text("Via")),
						h.Text(" + "),
						h.A(h.Href("https://picocss.com"), h.Text("Pico CSS")),
					),
				),

				theme.SignalDefinition(),
			)
		})
	})

	return v
}

