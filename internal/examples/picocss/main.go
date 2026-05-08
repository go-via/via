// Picocss demonstrates the picocss plugin with the typed-API surface.
//
//	go run ./internal/examples/picocss
package main

import (
	"fmt"
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
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

type Page struct {
	Visible via.State[int]
}

func (p *Page) Init(ctx *via.Ctx) error {
	p.Visible.Set(ctx, 3)
	return nil
}

func (p *Page) Inc(ctx *via.Ctx) error {
	if p.Visible.Get(ctx) < len(allFeatures) {
		p.Visible.Set(ctx, p.Visible.Get(ctx)+1)
	}
	return nil
}

func (p *Page) Dec(ctx *via.Ctx) error {
	if p.Visible.Get(ctx) > 1 {
		p.Visible.Set(ctx, p.Visible.Get(ctx)-1)
	}
	return nil
}

func (p *Page) View(ctx *via.Ctx) h.H {
	visible := allFeatures[:p.Visible.Get(ctx)]

	itemList := make([]h.H, 0, len(visible)+1)
	itemList = append(itemList, h.Style("padding:0;margin:0;display:flex;flex-direction:column;gap:0.75rem"))
	for _, f := range visible {
		itemList = append(itemList, h.Li(
			h.Style("list-style:none;padding:1rem 1.25rem;border:1px solid var(--pico-muted-border-color);border-radius:var(--pico-border-radius);display:flex;flex-direction:column;gap:0.25rem"),
			h.Strong(h.Text(f.Name)),
			h.Span(h.Style("color:var(--pico-muted-color)"), h.Text(f.Description)),
		))
	}

	themeRow := make([]h.H, 0, len(picocss.AllPicoThemes)+1)
	themeRow = append(themeRow, h.Style("display:flex;flex-wrap:wrap;gap:0.5rem;justify-content:center"))
	for _, t := range picocss.AllPicoThemes {
		themeRow = append(themeRow, h.Button(
			h.Style("margin:0;min-width:7rem"),
			h.DataClass("outline", "%s", fmt.Sprintf("%s!==%q", picocss.ThemeRef(), t.String())),
			h.DataClass("pico-color-"+t.String(), "%s", fmt.Sprintf("%s!==%q", picocss.ThemeRef(), t.String())),
			h.DataOnClick("%s", fmt.Sprintf("%s = %q", picocss.ThemeRef(), t.String())),
			h.Text(t.String()),
		))
	}

	dm := picocss.DarkModeRef()

	return h.Body(
		h.Nav(h.Class("container-fluid"),
			h.Ul(h.Li(h.Strong(h.Text("⚡ Via + Pico CSS")))),
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
						h.DataClass("outline", "%s", fmt.Sprintf("%s!=='light'", dm)),
						h.DataOnClick("%s", fmt.Sprintf("%s = 'light'", dm)),
					),
					h.Button(
						h.Style("margin:0;min-width:7rem"),
						h.Text("Dark"),
						h.DataClass("outline", "%s", fmt.Sprintf("%s!=='dark'", dm)),
						h.DataOnClick("%s", fmt.Sprintf("%s = 'dark'", dm)),
					),
					h.Button(
						h.Style("margin:0;min-width:7rem"),
						h.Text("System"),
						h.DataClass("outline", "%s", fmt.Sprintf("%s!=='system'", dm)),
						h.DataOnClick("%s", fmt.Sprintf("%s = 'system'", dm)),
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
					h.P(h.Textf("Showing %d of %d.", p.Visible.Get(ctx), len(allFeatures))),
				)),
				h.Div(
					h.Style("display:flex;align-items:center;justify-content:center;gap:1rem;margin-bottom:1.5rem"),
					h.Button(h.Style("margin:0;min-width:3.5rem"), h.Text("−"), on.Click(p.Dec)),
					h.Strong(
						h.Style("font-size:1.75rem;min-width:2.5rem;text-align:center"),
						h.Textf("%d", p.Visible.Get(ctx)),
					),
					h.Button(h.Style("margin:0;min-width:3.5rem"), h.Text("+"), on.Click(p.Inc)),
				),
				h.Ul(itemList...),
			),
		),
	)
}

func main() {
	app := via.New(
		via.WithTitle("Via + Pico CSS"),
		via.WithPlugins(
			picocss.Plugin(
				picocss.WithThemes(picocss.AllPicoThemes),
				picocss.WithDefaultTheme(picocss.PicoThemeAmber),
				picocss.WithColorClasses(),
			),
		),
	)
	via.Mount[Page](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
