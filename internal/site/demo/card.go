package demo

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/go-via/via/h"
)

// static/site.css selects tabs with :nth-of-type(1..3); an array makes a
// fourth tab a compile error until that changes.
const tabCount = 3

var tabs = [tabCount]string{"Live", "Source", "Inspect"}

// emptyPane is also inspector.js's EMPTY: the script rewrites the pane and
// puts this back when the log is cleared. demo/card_test.go pins the pair.
const emptyPane = "Interact with the demo to see requests, frames and signals here."

// Card renders a demo with Live, Source and Inspect tabs, plus a Show
// inspector checkbox that docks the inspector under the running demo. Tabs are
// radio inputs picked by :checked, so no signal is needed — the group name is
// what keeps two cards on one page from sharing a selection.
func Card(title string, prose h.H, child h.H, srcName string) h.H {
	group := "tab-" + group(title, srcName)
	panels := []h.H{
		h.Div(child),
		h.Div(Source(srcName)),
		h.Div(
			h.Div(h.Class("inspector"), h.DataIgnoreMorph(), h.Data("inspector", ""),
				h.P(h.Class("insp-empty"), h.Str(emptyPane))),
		),
	}

	ids := make([]string, tabCount)
	for i, t := range tabs {
		ids[i] = group + "-" + strings.ToLower(t)
	}
	inspectorID := group + "-inspector"

	// A fieldset so the three radios announce as one group; the legend names
	// it, and site.css hides the legend and the fieldset's own border.
	kids := []h.H{h.Class("demo-tabs"), h.Legend(h.Class("sr-only"), h.Str("Demo view"))}
	for i, id := range ids {
		kids = append(kids, h.Input(h.Type("radio"), h.Name(group), h.ID(id), h.Checked(i == 0)))
	}
	// A standalone checkbox, not part of the radio group: site.css counts it
	// as the 4th <input> and docks .inspector below whichever tab is showing
	// (site.css:268-272) rather than replacing it.
	kids = append(kids, h.Input(h.Type("checkbox"), h.ID(inspectorID)))
	for i, id := range ids {
		kids = append(kids, h.Label(h.For(id), h.Str(tabs[i])))
	}
	kids = append(kids, h.Label(h.For(inspectorID), h.Str("Show inspector")))

	return h.Article(h.Class("demo"),
		h.Header(h.H3(h.Str(title))),
		h.Div(h.Class("demo-prose"), prose),
		h.Fieldset(append(kids, panels...)...),
	)
}

// group ends in a digest so two cards whose titles slug the same ("Run it" and
// "Run-it") still get their own radio group.
func group(title, srcName string) string {
	sum := fnv.New32a()
	sum.Write([]byte(title + "\x00" + srcName))
	return slug(title+"-"+srcName) + "-" + strconv.FormatUint(uint64(sum.Sum32()), 16)
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
