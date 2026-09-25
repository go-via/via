// Package shell is the chrome every content page renders into: header,
// sidebar, main column, footer.
package shell

import (
	"cmp"

	"github.com/go-via/via/h"
)

const repo = "https://github.com/go-via/via"

// Page wraps a body in the site chrome. nav's heading becomes the <h1>, and
// the zero NavItem highlights nothing.
func Page(nav NavItem, body ...h.H) h.H {
	head := []h.H{h.ID("main"), h.Class("col"), h.H1(h.Str(cmp.Or(nav.Heading, nav.Title)))}
	return h.Div(h.Class("shell"),
		h.A(h.Href("#main"), h.Class("skip"), h.Str("Skip to content")),
		header(),
		sidebar(nav),
		h.Main(append(head, body...)...),
		footer(),
	)
}

// Landing is Page with the <h1> inside a hero card: art, heading, pitch, then
// the body.
func Landing(nav NavItem, art h.H, pitch string, body ...h.H) h.H {
	hero := h.Section(h.Class("hero"), art, h.H1(h.Str(cmp.Or(nav.Heading, nav.Title))), h.P(h.Class("pitch"), h.Str(pitch)))
	head := []h.H{h.ID("main"), h.Class("col"), hero}
	return h.Div(h.Class("shell"),
		h.A(h.Href("#main"), h.Class("skip"), h.Str("Skip to content")),
		header(),
		sidebar(nav),
		h.Main(append(head, body...)...),
		footer(),
	)
}

func header() h.H {
	return h.Header(h.Class("top"),
		h.A(h.Href("/"), h.Class("wordmark"),
			// 73×28 is the SVG's own 317.64:122 viewBox at the header's height; both
			// are set so the header does not reflow once the file arrives.
			h.Img(h.Src("/static/brand/wordmark-amber-dark.svg"), h.Alt("via"), h.Width(73), h.Height(28)),
		),
		h.A(h.Href(repo), h.Class("top-link"), h.Str("GitHub")),
	)
}

func sidebar(current NavItem) h.H {
	links := make([]h.H, 0, len(Nav))
	for _, it := range Nav {
		// site.css styles the current link off aria-current, so the state is
		// spelled once and as the thing a screen reader reads.
		link := []h.H{h.Href(it.Path), h.Class("nav-link"), h.Str(it.Title)}
		if it.Path == current.Path {
			link = append(link, h.Aria("current", "page"))
		}
		links = append(links, h.Li(h.A(link...)))
	}
	return h.Nav(h.Class("side"), h.Aria("label", "Documentation"), h.Ul(links...))
}

func footer() h.H {
	return h.Footer(h.Class("bottom"),
		h.Str("MIT licensed · "),
		h.A(h.Href(repo), h.Str("Source on GitHub")),
		h.Str(" · Created by "),
		h.A(h.Href("https://github.com/joaomdsg"), h.Str("joaomdsg")),
	)
}
