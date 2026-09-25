package demo

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// emptyPane is also inspector.js's EMPTY: the script rewrites the pane and
// puts this back when the log is cleared. demo/card_test.go pins the pair.
const emptyPane = "Use the demo to see its requests and patches here."

// Option adjusts one demo card.
type Option func(*config)

type config struct {
	wireOpen bool
	try      []h.H
}

// WireOpen starts the card's Wire pane open, for a demo whose point is the
// traffic rather than the widget.
func WireOpen() Option { return func(c *config) { c.wireOpen = true } }

// Try lists things to do with the demo, one item each, under the live child.
// Items are phrasing content: text, code, links.
func Try(items ...h.H) Option {
	return func(c *config) {
		if c.try != nil {
			panic("demo: Try given twice")
		}
		c.try = items
	}
}

// Card renders a running demo followed by its Wire pane and Source, each in a
// <details>, so the card needs no script or signal to open them. The Wire
// pane is filled by inspector.js, which the page loads through Assets; it
// finds the pane's demo through the enclosing .demo. heading is the card's own
// heading element, so the page's anchors and contents list include it.
func Card(heading h.H, prose h.H, child h.H, srcName string, opts ...Option) h.H {
	c := config{}
	for _, o := range opts {
		o(&c)
	}
	return h.Article(h.Class("demo"),
		h.Header(heading),
		h.Div(h.Class("demo-prose"), prose),
		h.Div(h.Class("demo-live"), child),
		c.tryList(),
		h.Details(h.Class("demo-more", "demo-wire"), h.Open(c.wireOpen),
			h.Summary(h.Str("Wire")),
			h.Div(h.Class("inspector"), h.DataIgnoreMorph(), h.Data("inspector", ""),
				h.P(h.Class("insp-empty"), h.Str(emptyPane))),
		),
		h.Details(h.Class("demo-more"),
			h.Summary(h.Str("Source")),
			Source(srcName),
		),
	)
}

func (c config) tryList() h.H {
	if len(c.try) == 0 {
		return nil
	}
	items := make([]h.H, 0, len(c.try))
	for _, it := range c.try {
		items = append(items, h.Li(it))
	}
	return h.Div(h.Class("demo-try"), h.P(h.Class("demo-try-title"), h.Str("Try this")), h.Ul(items...))
}

// Assets is what a page with a demo card declares in PageMeta().Assets: the
// script behind the Wire pane. asset turns "/static/x" into its served URL
// (shell.Site.Asset: fingerprinted, under the site's base). A page without a
// card leaves it out, and its head and CSP stay without it.
//
//	m := p.meta("…")
//	m.Assets = demo.Assets(p.site.Asset)
func Assets(asset func(path string) string) via.Assets {
	return via.Assets{Scripts: []via.Script{Script(asset)}}
}

// Script is Assets' one script, for a page that declares other assets too.
func Script(asset func(path string) string) via.Script {
	return via.Script{Src: asset("/static/inspector.js"), Defer: true}
}
