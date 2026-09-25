// Package shell is the chrome every content page renders into: top bar,
// sidebar, main column, footer.
package shell

import (
	"github.com/go-via/via/h"
	"go-via.dev/site/icon"
)

const repo = "https://github.com/go-via/via"

// ExtLink is a link off the site: it opens in a new tab and says so with an
// icon after its text.
func ExtLink(href string, kids ...h.H) h.H {
	a := append([]h.H{h.Href(href), h.Target("_blank"), h.Rel("noopener")}, kids...)
	return h.A(append(a, icon.External())...)
}

func (d *Doc) layout(main []h.H, foot, rail h.H) h.H {
	return h.Div(h.Class("shell"),
		h.A(h.Href("#main"), h.Class("skip"), h.Str("Skip to content")),
		// A checkbox, not a <details>: CSS cannot force a closed <details> open
		// at wide widths, and the checkbox needs no script either.
		h.Input(h.ID("nav-toggle"), h.Class("nav-toggle"), h.Type("checkbox"), h.Aria("controls", "drawer")),
		h.Header(h.Class("top"),
			h.Div(h.Class("top-brand"),
				h.Label(h.Class("menu-btn"), h.For("nav-toggle"), icon.Menu(), h.Span(h.Class("sr-only"), h.Str("Menu"))),
				h.A(h.Href(d.Href("/")), h.Class("wordmark"),
					// 73×28 is the SVG's own 317.64:122 viewBox at the header's height; both
					// are set so the header does not reflow once the file arrives.
					h.Img(h.Src(d.site.Asset("/static/brand/wordmark-amber-dark.svg")), h.Alt("via"), h.Width(73), h.Height(28)),
				),
			),
		),
		// Under 50rem this is the slide-in drawer, tools above the nav; from
		// 50rem it is display:contents and site.css pins the tools into the bar.
		// One copy of the search box, since it is a live child with a fixed
		// input id.
		h.Div(h.ID("drawer"), h.Class("drawer"),
			h.Div(h.Class("drawer-head"),
				h.A(h.Href(d.Href("/")), h.Class("wordmark"),
					h.Img(h.Src(d.site.Asset("/static/brand/wordmark-amber-dark.svg")), h.Alt("via"), h.Width(73), h.Height(28)),
				),
				h.Label(h.Class("drawer-close"), h.For("nav-toggle"), h.Aria("label", "Close menu"), icon.Close()),
			),
			h.Div(h.Class("top-tools"),
				d.search,
				h.Div(h.Class("top-links"),
					ExtLink(repo+"/releases", h.Str("Changelog")),
					ExtLink(repo, h.Str("GitHub")),
					ExtLink("https://pkg.go.dev/github.com/go-via/via", h.Str("pkg.go.dev")),
				),
			),
			h.Aside(h.Class("side"),
				h.Div(h.Class("side-inner"),
					h.Div(h.Class("side-body"), h.Div(h.Class("side-version"), d.versions()), d.sidebar()),
				),
			),
		),
		h.Label(h.Class("nav-backdrop"), h.For("nav-toggle"), h.Aria("hidden", "true")),
		h.Div(h.Class("main-wrap"),
			h.Div(h.Class("page"), h.Main(main...), foot, rail),
			footer(),
		),
	)
}

func (d *Doc) sidebar() h.H {
	groups := make([]h.H, 0, 2*len(Nav)+2)
	groups = append(groups, h.Class("nav"), h.Aria("label", "Documentation"))
	for _, g := range Nav {
		links := make([]h.H, 0, len(g.Items)+1)
		if g.Title == "" {
			links = append(links, h.Class("nav-top"))
		}
		for _, it := range g.Items {
			if it.URL != "" {
				links = append(links, h.Li(ExtLink(it.URL, h.Class("nav-link"), h.Str(it.Title))))
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
		if g.Title != "" {
			groups = append(groups, h.P(h.Class("nav-group"), h.Str(g.Title)))
		}
		groups = append(groups, h.Ul(links...))
	}
	return h.Nav(groups...)
}

func (d *Doc) versions() h.H {
	if len(d.site.Versions) < 2 {
		return nil
	}
	cur, _ := d.site.Current()
	latest := d.site.Latest()
	links := make([]h.H, 0, len(d.site.Versions))
	for _, v := range d.site.Versions {
		text := v.Label
		if v == latest {
			text = "latest (" + v.Label + ")"
		}
		if v.external() {
			links = append(links, h.Li(ExtLink(v.Href(d.path()), h.Str(text))))
			continue
		}
		a := []h.H{h.Href(v.Href(d.path())), h.Str(text)}
		if v.Base == d.site.Base {
			a = append(a, h.Aria("current", "page"))
		}
		links = append(links, h.Li(h.A(a...)))
	}
	label := cur.Label
	if cur == latest {
		label += " (latest)"
	}
	return h.Details(h.Class("versions"),
		h.Summary(h.Aria("label", "Version: "+label), h.Str(label)),
		h.Ul(links...),
	)
}

func footer() h.H {
	return h.Footer(h.Class("bottom"),
		h.Str("MIT licensed · "),
		ExtLink(repo, h.Str("Source on GitHub")),
		h.Str(" · Created by "),
		ExtLink("https://github.com/joaomdsg", h.Str("joaomdsg")),
	)
}
