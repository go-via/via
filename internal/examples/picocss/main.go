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
	v := via.New(
		via.WithTitle("Via + Pico CSS"),
		via.WithPlugins(
			picocss.Plugin(
				picocss.WithThemes(picocss.AllPicoThemes),
				picocss.WithDefaultTheme(picocss.PicoThemeAmber),
				picocss.WithColorClasses(),
			),
		),
	)

	v.Page("/", func(cmp *via.Cmp) {
		count := via.State(cmp, 3)

		increment := cmp.Action(func(ctx *via.Ctx) error {
			if count.Get(ctx) < len(allFeatures) {
				count.Set(ctx, count.Get(ctx)+1)
			}
			return nil
		})

		decrement := cmp.Action(func(ctx *via.Ctx) error {
			if count.Get(ctx) > 1 {
				count.Set(ctx, count.Get(ctx)-1)
			}
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			themeRef := picocss.ThemeSig().Ref()
			darkModeRef := picocss.DarkModeSig().Ref()
			visible := allFeatures[:count.Get(ctx)]

			itemList := make([]h.H, len(visible)+1)
			itemList[0] = h.Style("padding:0;margin:0;display:flex;flex-direction:column;gap:0.75rem")
			for i, f := range visible {
				itemList[i+1] = h.Li(
					h.Style("list-style:none;padding:1rem 1.25rem;border:1px solid var(--pico-muted-border-color);border-radius:var(--pico-border-radius);display:flex;flex-direction:column;gap:0.25rem"),
					h.Strong(h.Text(f.Name)),
					h.Span(h.Style("color:var(--pico-muted-color)"), h.Text(f.Description)),
				)
			}

			themeRow := make([]h.H, len(picocss.AllPicoThemes)+1)
			themeRow[0] = h.Style("display:flex;flex-wrap:wrap;gap:0.5rem;justify-content:center")
			for i, t := range picocss.AllPicoThemes {
				themeRow[i+1] = h.Button(
					h.DataClass("outline", "%s!==%q", themeRef, t.String()),
					h.DataClass("pico-color-"+t.String(), "%s!==%q", themeRef, t.String()),

					h.Style("margin:0;min-width:7rem"),
					h.DataOnClick("%s = %q", themeRef, t.String()),
					h.Text(t.String()),
				)
			}

			return h.Body(
				h.Nav(h.Class("container-fluid"),
					h.Ul(h.Li(h.Strong(h.Text("⚡ Via + Pico CSS")))),
					h.Ul(h.Li(h.A(
						h.Href("https://github.com/go-via/via/blob/main/internal/examples/picocss/main.go"),
						h.Attr("target", "_blank"),
						h.Rel("noopener"),
						h.AriaLabel("View source on GitHub"),
						h.Span(h.Text("View Source"), h.Style("margin-right: 0.3rem")),
						h.Raw(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z"/></svg>`),
					))),
				),
				h.Main(h.Class("container"),
					h.Section(
						h.Style("display:flex;flex-direction:column;align-items:center;gap:0.5rem;padding:3rem 1rem 2rem;text-align:center"),
						h.H1(h.Style("margin:0"), h.Text("Server-rendered, beautifully.")),
						h.P(
							h.Style("font-size:1.125rem;color:var(--pico-muted-color);margin:0;max-width:48ch"),
							h.Text("Pure Go. Zero JavaScript. Reactive by default."),
						),
					),
					h.Article(
						h.Header(h.HGroup(
							h.H3(h.Text("Dark Mode")),
							h.P(h.Text("Follow system or pick a side.")),
						)),
						h.Div(
							h.Style("display:flex;flex-wrap:wrap;gap:0.5rem;justify-content:center"),
							h.Button(
								h.Style("margin:0;min-width:7rem"),
								h.Text("Light"),
								h.DataClass("outline", "%s!=='light'", darkModeRef),
								h.DataOnClick("%s = 'light'", darkModeRef),
							),
							h.Button(
								h.Style("margin:0;min-width:7rem"),
								h.Text("Dark"),
								h.DataClass("outline", "%s!=='dark'", darkModeRef),
								h.DataOnClick("%s = 'dark'", darkModeRef),
							),
							h.Button(
								h.Style("margin:0;min-width:7rem"),
								h.Text("System"),
								h.DataClass("outline", "%s!=='system'", darkModeRef),
								h.DataOnClick("%s = 'system'", darkModeRef),
							),
						),
					),
					h.Article(
						h.Header(h.HGroup(
							h.H3(h.Text("Pick a theme")),
							h.P(h.Text("Hot-swap any Pico palette — server-side, zero flash.")),
						)),
						h.Div(themeRow...),
					),
					h.Article(
						h.Header(h.HGroup(
							h.H3(h.Text("Features")),
							h.P(h.Textf("Showing %d of %d.", count.Get(ctx), len(allFeatures))),
						)),
						h.Div(
							h.Style("display:flex;align-items:center;justify-content:center;gap:1rem;margin-bottom:1.5rem"),
							h.Button(h.Style("margin:0;min-width:3.5rem"), h.Text("−"), decrement.OnClick()),
							h.Strong(
								h.Style("font-size:1.75rem;min-width:2.5rem;text-align:center"),
								h.Textf("%d", count.Get(ctx)),
							),
							h.Button(h.Style("margin:0;min-width:3.5rem"), h.Text("+"), increment.OnClick()),
						),
						h.Ul(itemList...),
					),
				),
			)
		})
	})

	v.Start()
}
