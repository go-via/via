package demo

import (
	"strings"

	"github.com/go-via/via/h"
)

var tabs = []string{"Live", "Source", "Inspect"}

// Card is one documented demo: prose, the running child, its source, and the
// pane inspector.js fills.
//
// The tabs are radio inputs and labels, not script: the panels are picked by
// :checked in site.css, so the card needs no signal and no page-level state.
// srcName is a file of the demos package and also seeds the radio group name.
func Card(title string, prose h.H, child h.H, srcName string) h.H {
	group := "tab-" + slug(srcName)
	panels := []h.H{
		h.Div(child),
		h.Div(Source(srcName)),
		h.Div(
			// inspector.js replaces the contents; the paragraph is what the
			// pane says until it does, and if scripting is off.
			h.Div(h.Class("inspector"), h.DataIgnoreMorph(), h.Data("inspector", ""),
				h.P(h.Class("insp-empty"), h.Str("Interact with the demo to see requests, frames and signals here."))),
		),
	}

	kids := []h.H{h.Class("demo-tabs")}
	for i, t := range tabs {
		id := group + "-" + strings.ToLower(t)
		kids = append(kids, h.Input(h.Type("radio"), h.Name(group), h.ID(id), h.Checked(i == 0)))
	}
	for _, t := range tabs {
		id := group + "-" + strings.ToLower(t)
		kids = append(kids, h.Label(h.For(id), h.Str(t)))
	}

	return h.Article(h.Class("demo"),
		h.Header(h.H3(h.Str(title))),
		h.Div(h.Class("demo-prose"), prose),
		h.Div(append(kids, panels...)...),
	)
}

func slug(s string) string {
	s = strings.TrimSuffix(s, ".go")
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return '-'
	}, s)
}
