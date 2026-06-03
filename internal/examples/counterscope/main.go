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

// Brand palette from docs/assets/branding (amber bolt on ink).
const (
	amber = "#ffbf00"
	ink   = "#1b1e24"
	cream = "#efece4"
)

const bigNum = "font-size:7rem;font-weight:700;text-align:center;margin:0;" +
	"line-height:1;font-variant-numeric:tabular-nums;letter-spacing:-0.02em"

// boltLogo is the Via bolt mark (docs/assets/branding/bolt-amber.svg).
const boltLogo = `<svg width="26" height="28" viewBox="30 38 146 154" aria-hidden="true" style="vertical-align:middle"><path fill="` + amber + `" d="M 109,45 L 119,45 L 92,97 L 168,97 L 100,185 L 91,185 L 116,142 L 38,142 Z"/></svg>`

func brand() h.H {
	return h.A(h.Href("/"),
		h.Style("display:inline-flex;align-items:center;gap:0.6rem;text-decoration:none;color:inherit"),
		h.Raw(boltLogo),
		h.Strong(h.Style("font-size:1.3rem;letter-spacing:0.04em;font-weight:700"), h.Text("via")),
		h.Small(h.Style("opacity:0.55;margin-left:0.25rem;letter-spacing:0.08em;text-transform:uppercase;font-size:0.7rem"),
			h.Text("counter scope")),
	)
}

func badge(label string) h.H {
	return h.Small(
		h.Style("display:inline-block;padding:0.15rem 0.6rem;border-radius:999px;"+
			"font-size:0.7rem;font-weight:600;letter-spacing:0.1em;text-transform:uppercase;"+
			"background:"+amber+";color:"+ink),
		h.Text(label),
	)
}

func counterCard(title, scope, hint string, value h.H, btn h.H) h.H {
	return h.Article(
		h.Style("border-top:3px solid "+amber+";display:flex;flex-direction:column;gap:1rem"),
		h.HGroup(
			h.Div(h.Style("display:flex;align-items:center;justify-content:space-between"),
				h.H4(h.Style("margin:0"), h.Text(title)),
				badge(scope),
			),
			h.P(h.Small(h.Style("opacity:0.7"), h.Text(hint))),
		),
		h.P(h.Style(bigNum), value),
		h.Footer(h.Style("margin-top:auto"), btn),
	)
}

func (p *Page) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Header(h.Class("container"),
			h.Nav(
				h.Ul(h.Li(brand())),
				h.Ul(h.Li(h.Small(h.Style("opacity:0.6"), h.Text("tab vs app state, live over SSE")))),
			),
		),
		h.Main(h.Class("container"),
			h.P(h.Style("text-align:center;max-width:38rem;margin:0 auto 2rem"),
				h.Small(h.Text("Open this page in two windows side by side. "+
					"Local counts per tab; Shared syncs everywhere, instantly."))),
			h.Div(h.Class("grid"),
				counterCard("Local", "this tab", "StateTab[int] — independent per browser tab",
					h.Span(h.Style("color:"+cream), p.Local.Text(ctx)),
					h.Button(h.Class("outline"), h.Style("width:100%"), h.Text("+1"), on.Click(p.IncLocal)),
				),
				counterCard("Shared", "all tabs", "StateApp[int] — one value across every session",
					h.Span(h.Style("color:"+amber), p.Shared.Text(ctx)),
					h.Button(h.Style("width:100%"), h.Text("+1"), on.Click(p.IncShared)),
				),
			),
		),
		h.Footer(h.Class("container"),
			h.Hr(),
			h.P(h.Style("text-align:center;margin:0;display:flex;align-items:center;justify-content:center;gap:0.4rem"),
				h.Raw(`<svg width="14" height="15" viewBox="30 38 146 154" aria-hidden="true"><path fill="`+amber+`" d="M 109,45 L 119,45 L 92,97 L 168,97 L 100,185 L 91,185 L 116,142 L 38,142 Z"/></svg>`),
				h.Small(h.Text("Made with Via Hypermedia — go-via/via")),
			),
		),
	)
}

func main() {
	app := via.New(
		via.WithTitle("Via — Counter Scope"),
		via.WithPlugins(picocss.Plugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeAmber}))),
	)
	via.Mount[Page](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
