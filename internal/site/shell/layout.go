// Package shell is the chrome every content page renders into: header,
// sidebar, main column, footer.
package shell

import "github.com/go-via/via/h"

const repo = "https://github.com/go-via/via"

// Page wraps a page body in the site chrome. current is the nav entry this
// page is; the zero NavItem highlights nothing.
func Page(title string, current NavItem, body ...h.H) h.H {
	return h.Div(h.Class("shell"),
		header(),
		sidebar(current),
		h.Main(append([]h.H{h.Class("col"), h.H1(h.Str(title))}, body...)...),
		footer(),
	)
}

func header() h.H {
	return h.Header(h.Class("top"),
		h.A(h.Href("/"), h.Class("wordmark"),
			h.Img(h.Src("/static/brand/wordmark-amber-dark.svg"), h.Alt("via"), h.Height(28)),
		),
		h.A(h.Href(repo), h.Class("top-link"), h.Str("GitHub")),
	)
}

func sidebar(current NavItem) h.H {
	links := make([]h.H, 0, len(Nav))
	for _, it := range Nav {
		cls := "nav-link"
		if it.Path == current.Path {
			cls += " current"
		}
		links = append(links, h.Li(h.A(h.Href(it.Path), h.Class(cls), h.Str(it.Title))))
	}
	return h.Nav(h.Class("side"), h.Ul(links...))
}

func footer() h.H {
	return h.Footer(h.Class("bottom"),
		h.Str("MIT licensed · "),
		h.A(h.Href(repo), h.Str("github.com/go-via/via")),
	)
}
