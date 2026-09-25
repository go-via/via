// Package content is one mounted root page per file.
package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
)

// quickstart is a whole via program: one file, stdlib plus via, no build step.
const quickstart = `package main

import (
	"log"
	"net/http"
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type Counter struct{ n *atomic.Int64 }

func (c *Counter) Inc(ctx *via.Ctx) { c.n.Add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.n.Add(-1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.Button(on.Click(c.Dec), h.Str("-")),
		h.H1(h.Str(c.n.Load())),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}

func main() {
	r := via.Handler(Counter{n: new(atomic.Int64)})
	err := http.ListenAndServe(":8080", r)
	r.Close()
	log.Fatal(err)
}
`

// Landing is the front page.
type Landing struct{ page }

func NewLanding(env Env) Landing { return Landing{page: newPage("/", env)} }

func (p *Landing) PageMeta() via.Meta {
	return p.meta("via is a Go library for server-rendered, live web UI: HTML from Go functions, actions as methods, no JavaScript to write.")
}

func (p *Landing) View() h.H {
	d := p.doc()
	return d.Landing(
		// Animated PNG, the same hero as the v0.7 docs; ~840 KB, so it is the
		// one asset on the page that is not an SVG.
		h.Img(h.Class("hero-art"), h.Src(d.Href("/static/brand/punch-dark.png")), h.Alt("via"), h.Width(340), h.Height(340)),
		"A page is a struct, a click is a method call, and via streams what changed back to the tab. "+
			"No JavaScript to write, no build step, plain HTTP until something on the page ticks.",

		d.H2("The whole program"),
		demo.Code(quickstart),

		d.H2("Why"),
		h.Ul(
			h.Li(h.B(h.Str("No JavaScript to write. ")), h.Str("Markup is Go, handlers are methods, and the wire protocol is via's problem.")),
			h.Li(h.B(h.Str("Plain until it is not. ")), h.Str("A page is a request and a response until something on it ticks, listens, or displays server state; then it streams, per tab.")),
			h.Li(h.B(h.Str("Safe by construction. ")), h.Str("Output is escaped with no opt-out, only what a render bound is dispatchable, and every page carries a derived CSP.")),
		),
	)
}
