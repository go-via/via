// Package content is one mounted root page per file.
package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/shell"
)

// quickstart is a whole via program: one file, stdlib plus via, no build step.
const quickstart = `package main

import (
	"log"
	"net/http"
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type Counter struct{ n *atomic.Int64 }

func (c *Counter) Inc(ctx *via.Ctx) { c.n.Add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.n.Add(-1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.Button(via.On("click", c.Dec), h.Str("-")),
		h.H1(h.Str(c.n.Load())),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

func main() {
	r := via.Handler(Counter{n: new(atomic.Int64)})
	defer r.Close()
	log.Fatal(http.ListenAndServe(":8080", r))
}
`

// Landing is the front page.
type Landing struct{}

func (p *Landing) PageMeta() via.Meta {
	return shell.Meta(shell.NavFor("/"), "via is a Go library for server-rendered, live web UI: HTML from Go functions, actions as methods, no JavaScript to write.")
}

func (p *Landing) View() h.H {
	return shell.Page(shell.NavFor("/"),
		h.Section(h.Class("hero"),
			h.Img(h.Class("hero-art"), h.Src("/static/brand/bolt-amber.svg"), h.Alt(""), h.Width(146), h.Height(154)),
			h.Img(h.Class("hero-mark"), h.Src("/static/brand/wordmark-amber-dark.svg"), h.Alt("via"), h.Height(44)),
			h.P(h.Class("pitch"), h.Str("Write the page as Go functions, wire a click to a method, "+
				"and via streams the parts that changed back to the browser.")),
		),

		h.H2(h.Str("The whole program")),
		demo.Code(quickstart),

		h.H2(h.Str("Why")),
		h.Ul(
			h.Li(h.B(h.Str("No JavaScript to write. ")), h.Str("Markup is Go, handlers are methods, and the wire protocol is via's problem.")),
			h.Li(h.B(h.Str("Plain until it is not. ")), h.Str("A page is a request and a response until something on it ticks, listens, or displays server state; then it streams, per tab.")),
			h.Li(h.B(h.Str("Safe by construction. ")), h.Str("Output is escaped with no opt-out, only what a render bound is dispatchable, and every page carries a derived CSP.")),
		),

		h.P(
			h.A(h.Href("/actions"), h.Str("Start with actions")),
			h.Str(" · "),
			h.A(h.Href("https://github.com/go-via/via"), h.Str("Source on GitHub")),
		),
	)
}
