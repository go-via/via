package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/search"
	"go-via.dev/site/shell"
	"go-via.dev/site/snippet"
)

// Styleguide is /_ui: every shared component once, for design review. It is
// outside the nav, so neither the sidebar, the pager nor the search index
// reach it, and it asks search engines to skip it.
type Styleguide struct {
	page
	Counter demos.Counter
}

func NewStyleguide(env Env) Styleguide {
	nav := shell.NavItem{Title: "Components", Path: "/_ui", File: "styleguide.go"}
	return Styleguide{page: page{nav: nav, site: env.Site, Search: search.NewBox(env.Index)}}
}

func (p *Styleguide) PageMeta() via.Meta {
	m := p.meta("The site's shared content components, for design review.")
	m.Robots = "noindex"
	m.Assets = demo.Assets(p.site.Asset)
	return m
}

func (p *Styleguide) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("Every component a page is built from, in the order a page tends to use them. Prose sits at the reading width; "),
			Code("go vet ./..."), h.Str(" compiles every Go block here, and "), Kbd("Ctrl", "K"), h.Str(" focuses search.")),

		d.H2("Code from a compiled file"),
		h.P(h.Str("snippet.Show renders a whole file under snippet/src, labelled with its name.")),
		snippet.Show("counter/main.go"),
		h.P(h.Str("snippet.Region renders one named region, dedented, and Mark highlights the lines that carry the point.")),
		snippet.Region("counter/main.go", "view", snippet.Mark("on.Click")),

		d.H2("Plain files and commands"),
		snippet.Text("Caddyfile", "go-via.dev {\n\treverse_proxy 127.0.0.1:8080\n}\n"),
		snippet.Text("", "go get github.com/go-via/via"),

		d.H2("Callouts"),
		Callout(Note, "Plain until it is not", h.P(h.Str("A page is a request and a response until something on it ticks, listens or displays server state; then it streams, per tab."))),
		Callout(Warning, "Set the origin in production", h.P(h.Str("Without "), API("via.WithTrustedOrigin"), h.Str(" every origin is accepted, so another site can post to your actions."))),
		Callout(Caveat, "One process", h.P(h.Str("State lives in one process's memory, so two replicas behind a load balancer do not share it."))),

		d.H2("Steps"),
		Steps(
			Step("Add the module", snippet.Text("", "go get github.com/go-via/via")),
			Step("Write a composition", snippet.Region("counter/main.go", "teaser", snippet.Title("main.go"))),
			Step("Run it", h.P(h.Str("Open "), Code("http://localhost:8080"), h.Str(" and click."))),
		),

		d.H2("Quick links"),
		CardGrid(
			LinkCard(d.Href("/start"), "Getting started", "Install, one page, one click."),
			LinkCard(d.Href("/actions"), "Actions", "Clicks and submits as Go methods."),
			LinkCard(d.Href("/live"), "Live updates", "Ticks, topics and one stream per tab."),
			LinkCard(d.Href("/reference"), "API reference", "Every exported name, one line each."),
		),

		d.H2("Compare"),
		Compare("Whole program", snippet.Region("counter/main.go", "teaser", snippet.Title("")),
			"The view alone", snippet.Region("counter/main.go", "view", snippet.Title(""))),

		d.H2("Table"),
		table([]string{"Status", "Means"},
			[]h.H{Code("410"), h.Str("The server no longer holds this tab's composition.")},
			[]h.H{Code("503"), h.Str("The tab's goroutine is busy or the store did not answer.")},
		),

		d.H2("Demo card"),
		demo.Card(d.H3("Counter"),
			h.P(h.Str("A live unit with its Wire pane open: each click is one POST and one patch.")),
			via.Child(p.Counter), "counter.go",
			demo.WireOpen(),
			demo.Try(h.Str("Click + and watch the POST and its patch arrive."), h.Str("Open Source: the file shown is the file that runs."))),
	)
}
