// Package content is one mounted root page per file.
package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
)

// Landing is the front page.
type Landing struct {
	page
	Counter demos.LandingCounter
}

func NewLanding(env Env) Landing {
	return Landing{page: newPage("/", env), Counter: demos.NewLandingCounter()}
}

func (p *Landing) PageMeta() via.Meta {
	m := p.meta("via is a Go library for server-rendered, live web UI: HTML from Go functions, actions as methods, no JavaScript to write.")
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Landing) View() h.H {
	d := p.doc()
	return d.Landing(
		// Animated PNG, the same hero as the v0.7 docs; ~840 KB, so it is the
		// one asset on the page that is not an SVG.
		h.Img(h.Class("hero-art"), h.Src(p.site.Asset("/static/brand/punch-dark.png")), h.Alt("via"), h.Width(440), h.Height(250)),
		"A page is a struct, a click is a method call, and via streams what changed back to the tab. "+
			"No JavaScript to write, no build step, plain HTTP until something on the page ticks.",

		d.H2("A counter"),
		h.P(h.Str("The struct is the page and each button calls a method. The count is one "), Code("*atomic.Int64"),
			h.Str(" shared by every visitor, so the number you see includes everyone else's clicks.")),
		snippet.Region("counter/main.go", "teaser"),
		demo.Card(d.H3("Running"),
			h.P(h.Str("The same counter on this page. Nothing in its View is live, so a click is one POST and its response is the patch.")),
			via.Child(p.Counter), "landing_counter.go", demo.WireOpen(),
			demo.Try(h.Str("Click + and read the Wire pane: the POST, its status, the patched element."))),
		h.P(h.A(h.Class("btn"), h.Href(d.Href("/start")+"#program"), h.Str("See the full program →"))),

		d.H2("Three kinds of field"),
		table([]string{"Field type", "Value lives in", "Server code"},
			[]h.H{APIText("via.Signal", "Signal"), h.Str("the browser; sent with every action POST"), h.Str("reads it with Get, changes it with Set")},
			[]h.H{APIText("via.SignalCS", "SignalCS"), h.Str("the browser only; dropped from every POST"), h.Str("cannot read or write it: the type has no Get or Set")},
			[]h.H{APIText("via.State", "State"), h.Str("the server, per tab"), h.Str("owns it; rendering it with Display makes the unit live, and a Set is pushed")},
		),
		h.P(h.Str("Where a value lives is the type of its field, so the compiler holds the client/server split: "+
			"server code that reads a "), API("via.SignalCS"), h.Str(" does not build, and a "), API("via.State"),
			h.Str(" has no "), Code("Ref"), h.Str(" for a client expression to reach. The full table is "),
			h.A(h.Href(d.Href("/signals")+"#where-state-lives"), h.Str("Where state lives")), h.Str(", and "),
			h.A(h.Href(d.Href("/why")), h.Str("Why Via")), h.Str(" makes the longer case.")),

		d.H2("Read next"),
		CardGrid(
			LinkCard(d.Href("/start"), "Getting started", "Install, run the counter, make it live."),
			LinkCard(d.Href("/actions"), "Actions", "Clicks and forms as methods."),
			LinkCard(d.Href("/signals"), "Signals", "State that stays in the browser."),
			LinkCard(d.Href("/live"), "Live state", "The server pushing to the tab."),
			LinkCard(d.Href("/examples"), "Examples", "Whole programs to copy."),
			LinkCard(d.Href("/reference"), "API reference", "Every exported name."),
		),
	)
}
