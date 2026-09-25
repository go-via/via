package content

import (
	"github.com/go-via/via/h"
)

// Tone is a Callout's kind, which sets its label and its colour.
type Tone int

const (
	// Note is context a reader may want and can skip.
	Note Tone = iota
	// Warning is something that breaks if the reader ignores it.
	Warning
	// Caveat is a limit or cost of the design, stated so nobody finds it in
	// production.
	Caveat
)

var tones = [...]struct{ class, label string }{
	Note:    {"callout-note", "Note"},
	Warning: {"callout-warning", "Warning"},
	Caveat:  {"callout-caveat", "Caveat"},
}

// Callout sets a short aside off from the prose. title is a few words that
// say what the aside is about; it follows the tone's label.
//
//	Callout(Caveat, "One process", h.P(h.Str("State lives in this process's memory.")))
func Callout(t Tone, title string, body ...h.H) h.H {
	tone := tones[t]
	kids := []h.H{h.Class("callout", tone.class), h.Role("note"),
		h.P(h.Class("callout-title"), h.Span(h.Class("callout-label"), h.Str(tone.label)), h.Str(title))}
	return h.Aside(append(kids, body...)...)
}

// Steps is a numbered procedure. Each child is a Step.
//
//	Steps(
//		Step("Install", snippet.Text("", "go get github.com/go-via/via")),
//		Step("Run it", h.P(h.Str("…"))),
//	)
func Steps(steps ...h.H) h.H { return h.Ol(append([]h.H{h.Class("steps")}, steps...)...) }

// Step is one item of Steps: a short imperative title, then its prose and
// code.
func Step(title string, body ...h.H) h.H {
	return h.Li(append([]h.H{h.Class("step"), h.P(h.Class("step-title"), h.Str(title))}, body...)...)
}

// CardGrid lays LinkCards out in as many columns as fit.
func CardGrid(cards ...h.H) h.H { return h.Div(append([]h.H{h.Class("card-grid")}, cards...)...) }

// LinkCard is one quick link: a title, one line on what the reader finds
// there, and the whole card is the link.
//
//	LinkCard(d.Href("/start"), "Getting started", "Install, one page, one click.")
func LinkCard(href, title, line string) h.H {
	return h.A(h.Class("link-card"), h.Href(href),
		h.Span(h.Class("link-card-title"), h.Str(title),
			h.Span(h.Class("link-card-arrow"), h.Aria("hidden", "true"), h.Str("→"))),
		h.Span(h.Class("link-card-line"), h.Str(line)),
	)
}

// Compare puts two blocks one above the other, each under its label: before
// and after, or another framework and via. Stacked at every width, because the
// text column is too narrow for two halves of code.
//
//	Compare("v0.7", snippet.Region("migrate/v07.go", "counter", snippet.Title("")),
//		"v0.8", snippet.Region("migrate/v08.go", "counter", snippet.Title("")))
func Compare(leftLabel string, left h.H, rightLabel string, right h.H) h.H {
	return h.Div(h.Class("compare"),
		h.Figure(h.Class("compare-side"), h.Figcaption(h.Str(leftLabel)), left),
		h.Figure(h.Class("compare-side"), h.Figcaption(h.Str(rightLabel)), right),
	)
}

// Kbd is a key chord: Kbd("Ctrl", "K") renders Ctrl+K as two keys.
func Kbd(keys ...string) h.H {
	kids := make([]h.H, 0, 2*len(keys))
	for i, k := range keys {
		if i > 0 {
			kids = append(kids, h.Str("+"))
		}
		kids = append(kids, h.Kbd(h.Str(k)))
	}
	return h.Span(append([]h.H{h.Class("kbd")}, kids...)...)
}

// Code is inline code for a name that has no /reference row: a command, a
// file, a field. An exported via name takes API instead, which links it.
func Code(s string) h.H { return h.Code(h.Str(s)) }
