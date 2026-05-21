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
	Local  via.StateTab[int]
	Shared via.StateApp[int]
}

func (p *Page) IncLocal(ctx *via.Ctx) {
	p.Local.Update(ctx, func(n int) int { return n + 1 })
}
func (p *Page) IncShared(ctx *via.Ctx) {
	p.Shared.Update(ctx, func(n int) int { return n + 1 })
}

func (p *Page) View(ctx *via.CtxR) h.H {
	return h.Main(h.Class("container"),
		h.Article(
			h.H2(h.Text("Local (tab-scoped)")),
			h.P(h.Text("Count: "), p.Local.Text()),
			h.Button(h.Text("+"), on.Click(p.IncLocal)),
		),
		h.Article(
			h.H2(h.Text("Shared (app-scoped)")),
			h.P(h.Text("Count: "), p.Shared.Text(ctx)),
			h.Button(h.Text("+"), on.Click(p.IncShared)),
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
