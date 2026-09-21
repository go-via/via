package demo

import (
	"strings"

	"github.com/go-via/via/h"
)

// static/site.css selects tabs with :nth-of-type(1..3); an array makes a
// fourth tab a compile error until that changes.
const tabCount = 3

var tabs = [tabCount]string{"Live", "Source", "Inspect"}

// Card renders a demo with Live, Source and Inspect tabs. Tabs are radio
// inputs picked by :checked, so no signal is needed. The group name includes
// title because one source file can back several cards.
func Card(title string, prose h.H, child h.H, srcName string) h.H {
	group := "tab-" + slug(title+"-"+srcName)
	panels := []h.H{
		h.Div(child),
		h.Div(Source(srcName)),
		h.Div(
			h.Div(h.Class("inspector"), h.DataIgnoreMorph(), h.Data("inspector", ""),
				h.P(h.Class("insp-empty"), h.Str("Interact with the demo to see requests, frames and signals here."))),
		),
	}

	ids := make([]string, tabCount)
	for i, t := range tabs {
		ids[i] = group + "-" + strings.ToLower(t)
	}

	kids := []h.H{h.Class("demo-tabs")}
	for i, id := range ids {
		kids = append(kids, h.Input(h.Type("radio"), h.Name(group), h.ID(id), h.Checked(i == 0)))
	}
	for i, id := range ids {
		kids = append(kids, h.Label(h.For(id), h.Str(tabs[i])))
	}

	return h.Article(h.Class("demo"),
		h.Header(h.H3(h.Str(title))),
		h.Div(h.Class("demo-prose"), prose),
		h.Div(append(kids, panels...)...),
	)
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSuffix(s, ".go"))
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		}
		return '-'
	}, s)
}
