// Counterscope demos the difference between tab-local and app-scoped
// state. Open two browsers side-by-side: the "Local" counter increments
// independently in each tab; the "Shared" counter syncs across every
// open session.
//
//	go run ./internal/examples/counterscope
//	open http://localhost:3000
package main

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/picocss"
)

type Page struct {
	Local  via.StateTabNum[int]
	Shared via.StateAppNum[int]
}

func (p *Page) IncLocal(ctx *via.Ctx)  { p.Local.Op(ctx).Inc() }
func (p *Page) IncShared(ctx *via.Ctx) { p.Shared.Op(ctx).Inc() }

const bigNum = "font-size:6rem;font-weight:700;text-align:center;margin:0;line-height:1;font-variant-numeric:tabular-nums"

const viaLogo = `<svg width="20" height="44" viewBox="30 -8 144 336" aria-hidden="true" style="vertical-align:middle"><path d="M 142,0 L 92,130 L 168,130 L 58,320 L 116,190 L 38,190 Z" fill="none" stroke="currentColor" stroke-width="10" stroke-linecap="square" stroke-linejoin="miter" stroke-miterlimit="10"/></svg>`

func brand() h.H {
	return h.A(h.Href("/"), h.Style("display:inline-flex;align-items:center;gap:0.5rem;text-decoration:none;color:inherit"),
		h.Raw(viaLogo),
		h.Strong(h.Style("font-size:1.15rem;letter-spacing:0.02em"), h.Text("Via")),
		h.Small(h.Style("opacity:0.55;margin-left:0.25rem"), h.Text("hypermedia")),
	)
}

func (p *Page) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Header(h.Class("container"),
			h.Nav(
				h.Ul(h.Li(brand())),
				h.Ul(h.Li(h.Small(h.Text("Counter Scope — tab vs app state")))),
			),
		),
		h.Main(h.Class("container"),
			h.Div(h.Class("grid"),
				h.Article(
					h.HGroup(
						h.H4(h.Text("Local")),
						h.P(h.Small(h.Text("Per-tab — StateTab[int]"))),
					),
					h.P(h.Style(bigNum), p.Local.Text(ctx)),
					h.Footer(h.Button(h.Style("width:100%"), h.Text("+1"), on.Click(p.IncLocal))),
				),
				h.Article(
					h.HGroup(
						h.H4(h.Text("Shared")),
						h.P(h.Small(h.Text("Across all tabs — StateApp[int]"))),
					),
					h.P(h.Style(bigNum), p.Shared.Text(ctx)),
					h.Footer(h.Button(h.Style("width:100%"), h.Text("+1"), on.Click(p.IncShared))),
				),
			),
		),
		h.Footer(h.Class("container"),
			h.Hr(),
			h.P(h.Style("text-align:center;margin:0"),
				h.Small(h.Text("Made with ❤️ Via Hypermedia")),
			),
		),
	)
}

func main() {
	app := via.New(
		via.WithTitle("Counter Scope"),
		via.WithPlugins(picocss.Plugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeAmber}))),
	)
	via.Mount[Page](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
