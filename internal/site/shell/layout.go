// Package shell is the chrome every content page renders into: sidebar, main
// column, footer.
package shell

import "github.com/go-via/via/h"

const repo = "https://github.com/go-via/via"

func (d *Doc) layout(main []h.H) h.H {
	return h.Div(h.Class("shell"),
		h.A(h.Href("#main"), h.Class("skip"), h.Str("Skip to content")),
		// A checkbox, not a <details>: CSS cannot force a closed <details> open
		// at wide widths, and the checkbox needs no script either.
		h.Input(h.ID("nav-toggle"), h.Class("nav-toggle"), h.Type("checkbox"), h.Aria("controls", "side-body")),
		h.Aside(h.Class("side"),
			h.Div(h.Class("side-inner"),
				h.Div(h.Class("side-head"),
					h.A(h.Href(d.Href("/")), h.Class("wordmark"),
						// 73×28 is the SVG's own 317.64:122 viewBox at the header's height; both
						// are set so the header does not reflow once the file arrives.
						h.Img(h.Src(d.Href("/static/brand/wordmark-amber-dark.svg")), h.Alt("via"), h.Width(73), h.Height(28)),
					),
					h.Label(h.Class("menu-btn"), h.For("nav-toggle"), h.Str("Menu")),
					d.versions(),
				),
				h.Div(h.ID("side-body"), h.Class("side-body"),
					d.search,
					d.sidebar(),
				),
			),
		),
		h.Div(h.Class("main-wrap"),
			h.Main(main...),
			footer(),
		),
	)
}

func (d *Doc) sidebar() h.H {
	groups := make([]h.H, 0, 2*len(Nav)+2)
	groups = append(groups, h.Class("nav"), h.Aria("label", "Documentation"))
	for _, g := range Nav {
		links := make([]h.H, 0, len(g.Items))
		for _, it := range g.Items {
			if it.URL != "" {
				links = append(links, h.Li(h.A(h.Href(it.URL), h.Class("nav-link nav-ext"), h.Str(it.Title))))
				continue
			}
			// site.css styles the current link off aria-current, so the state is
			// spelled once and as the thing a screen reader reads.
			link := []h.H{h.Href(d.Href(it.Path)), h.Class("nav-link"), h.Str(it.Title)}
			if it.Path == d.nav.Path {
				link = append(link, h.Aria("current", "page"))
			}
			links = append(links, h.Li(h.A(link...)))
		}
		groups = append(groups, h.P(h.Class("nav-group"), h.Str(g.Title)), h.Ul(links...))
	}
	return h.Nav(groups...)
}

func (d *Doc) versions() h.H {
	if len(d.site.Versions) < 2 {
		return nil
	}
	cur, _ := d.site.Current()
	links := make([]h.H, 0, len(d.site.Versions))
	for _, v := range d.site.Versions {
		a := []h.H{h.Href(v.Href(d.path())), h.Str(v.Label)}
		if v.Base == d.site.Base {
			a = append(a, h.Aria("current", "page"))
		}
		links = append(links, h.Li(h.A(a...)))
	}
	return h.Details(h.Class("versions"),
		h.Summary(h.Aria("label", "Version: "+cur.Label), h.Str(cur.Label)),
		h.Ul(links...),
	)
}

func footer() h.H {
	return h.Footer(h.Class("bottom"),
		h.Str("MIT licensed · "),
		h.A(h.Href(repo), h.Str("Source on GitHub")),
		h.Str(" · Created by "),
		h.A(h.Href("https://github.com/joaomdsg"), h.Str("joaomdsg")),
	)
}
