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
	{"Server-Sent Events", "Efficient bidirectional communication without WebSockets."},
	{"State Management", "Simple yet powerful server-side state with automatic sync."},
	{"Path Parameters", "Dynamic routes with typed path parameters."},
	{"Middleware", "Chain handlers for logging, auth, and more."},
	{"HTML DSL", "Type-safe HTML building with the h package."},
	{"Streaming", "Progressive content delivery with SSE streaming."},
}

func main() {

	v := via.New()

	plugin := picocss.New(picocss.Options{
		Themes:       picocss.AllThemes,
		DefaultTheme: "blue",
		ColorClasses: true,
		DarkMode:     true,
	})
	plugin.Register(v)

	v.Page("/", func(c *via.Composition) {
		theme := picocss.Theme(c, picocss.Options{
			Themes:       picocss.AllThemes,
			DefaultTheme: "blue",
			ColorClasses: true,
		})

		featureCount := via.State(c, 3)

		incrementFeature := via.Action(c, func(ctx *via.Context) {
			current := featureCount.Get(ctx)
			if current < len(allFeatures) {
				featureCount.Set(ctx, current+1)
			}
		})

		decrementFeature := via.Action(c, func(ctx *via.Context) {
			current := featureCount.Get(ctx)
			if current > 0 {
				featureCount.Set(ctx, current-1)
			}
		})

		c.View(func(ctx *via.Context) h.H {
			visibleFeatures := allFeatures
			count := featureCount.Get(ctx)
			if count > 0 && count <= len(allFeatures) {
				visibleFeatures = allFeatures[:count]
			}

			featureItems := func() []h.H {
				var items []h.H
				for _, f := range visibleFeatures {
					items = append(items,
						h.Li(
							h.Strong(h.Text(f.Name+" - ")),
							h.Text(f.Description),
						),
					)
				}
				return items
			}

			return h.Body(
				h.Header(
					h.Class("hero"),
					h.Nav(
						h.Class("container"),
						h.Ul(
							h.Li(
								h.A(
									h.Class("contrast"),
									h.Href("./"),
									h.Strong(h.Text("⚡ Via")),
								),
							),
						),
						h.Ul(
							h.Li(
								h.Button(
									h.Data("on:click", "$_picoDarkMode = !$_picoDarkMode"),
									h.Attr("aria-label", "Toggle dark mode"),
									h.Text("☀️"),
								),
							),
						),
					),
					h.Header(
						h.Class("container"),
						h.HGroup(
							h.H1(h.Text("Build Reactive Web Apps with Go")),
							h.P(h.Class("lead"), h.Text("Write pure Go. No JavaScript. Real-time via SSE.")),
						),
					),
				),

				h.Main(
					h.Class("container"),
					h.Article(
						h.H2(h.Text("Choose Theme")),
						h.Div(
							h.Style("display: flex; flex-wrap: wrap; gap: 0.5rem;"),
							h.Map(picocss.AllThemes, func(themeName string) h.H {
								return h.Button(
									h.Class("pico-background-"+themeName),
									h.DataOnClick("$_picoTheme = '"+themeName+"'"),
									h.Textf("%s", themeName),
								)
							}),
						),
					),
					h.Article(
						h.H2(h.Text("Features")),
						h.P(h.Text("Via provides everything you need to build modern web apps:")),
						h.P(
							h.Button(
								h.Text("-"),
								decrementFeature.OnClick(),
							),
							h.Textf(" %d/%d ", count, len(allFeatures)),
							h.Button(
								h.Text("+"),
								incrementFeature.OnClick(),
							),
						),
						h.Ul(featureItems()...),
					),
				),

				h.Footer(
					h.Class("container"),
					h.Small(
						h.Text("Built with "),
						h.A(h.Text("Via"), h.Href("https://github.com/go-via/via")),
						h.Text(" + "),
						h.A(h.Text("Pico CSS"), h.Href("https://picocss.com")),
					),
				),

				theme.SignalDefinition(),
			)
		})
	})

	v.Start()
}
