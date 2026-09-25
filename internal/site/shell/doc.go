package shell

import (
	"cmp"
	"strconv"
	"strings"

	"github.com/go-via/via/h"
)

// Doc is one render of one page: it hands out heading anchors and remembers
// them, so the page's contents list is built from the headings it drew.
type Doc struct {
	site   *Site
	nav    NavItem
	search h.H
	heads  []heading
	used   map[string]int
}

type heading struct {
	level     int
	id, title string
}

// Doc starts a render of the page nav names. search is the sidebar's search
// box; nil leaves it out.
func (s *Site) Doc(nav NavItem, search h.H) *Doc {
	return &Doc{site: s, nav: nav, search: search, used: map[string]int{}}
}

// Slug is title as a fragment id: lower-case ASCII letters and digits, every
// other run of characters one "-".
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}

// H2 is a section heading with its own anchor.
func (d *Doc) H2(title string) h.H { return d.heading(2, title) }

// H3 is a subsection heading with its own anchor.
func (d *Doc) H3(title string) h.H { return d.heading(3, title) }

func (d *Doc) heading(level int, title string) h.H {
	id := Slug(title)
	d.used[id]++
	if n := d.used[id]; n > 1 {
		id += "-" + strconv.Itoa(n)
	}
	d.heads = append(d.heads, heading{level: level, id: id, title: title})
	kids := []h.H{
		h.ID(id),
		h.A(h.Class("anchor"), h.Href("#"+id), h.Aria("label", "Link to this section"), h.Str("#")),
		h.Str(title),
	}
	if level == 2 {
		return h.H2(kids...)
	}
	return h.H3(kids...)
}

// Href is path on this build of the site.
func (d *Doc) Href(path string) string { return d.site.Href(path) }

// Page renders body under the page's heading in the site chrome. Go evaluates
// every argument, so every d.H2 in body, before the method runs: by then
// d.heads is complete and the contents list sees them all.
func (d *Doc) Page(body ...h.H) h.H {
	main := []h.H{h.ID("main"), h.Class("col"),
		d.oldVersion(), d.crumbs(), h.H1(h.Str(cmp.Or(d.nav.Heading, d.nav.Title))), d.toc()}
	main = append(main, body...)
	main = append(main, d.next())
	return d.layout(main)
}

// Landing is Page with the heading inside a hero card: art, heading, pitch,
// the Get started and GitHub links, then the body.
func (d *Doc) Landing(art h.H, pitch string, body ...h.H) h.H {
	hero := h.Section(h.Class("hero"), art,
		h.H1(h.Str(cmp.Or(d.nav.Heading, d.nav.Title))),
		h.P(h.Class("pitch"), h.Str(pitch)),
		h.Div(h.Class("hero-actions"),
			h.A(h.Class("btn btn-primary"), h.Href(d.Href("/start")), h.Str("Get started")),
			h.A(h.Class("btn"), h.Href(repo), h.Str("GitHub")),
		),
	)
	main := []h.H{h.ID("main"), h.Class("col"), d.oldVersion(), hero, d.toc()}
	main = append(main, body...)
	main = append(main, d.next())
	return d.layout(main)
}

func (d *Doc) crumbs() h.H {
	if d.nav.Group == "" || d.nav.Path == "/" {
		return nil
	}
	return h.Nav(h.Class("crumbs"), h.Aria("label", "Breadcrumb"),
		h.Str(d.nav.Group+" › "+d.nav.Title))
}

func (d *Doc) toc() h.H {
	var n int
	for _, hd := range d.heads {
		if hd.level == 2 {
			n++
		}
	}
	if n < 3 {
		return nil
	}
	var items []h.H
	var subs []h.H
	var open []h.H
	flush := func() {
		if open == nil {
			return
		}
		if len(subs) > 0 {
			open = append(open, h.Ul(subs...))
		}
		items = append(items, h.Li(open...))
		open, subs = nil, nil
	}
	for _, hd := range d.heads {
		link := h.A(h.Href("#"+hd.id), h.Str(hd.title))
		if hd.level == 2 {
			flush()
			open = []h.H{link}
			continue
		}
		if open == nil {
			items = append(items, h.Li(link))
			continue
		}
		subs = append(subs, h.Li(link))
	}
	flush()
	return h.Nav(h.Class("toc"), h.Aria("label", "On this page"),
		h.P(h.Class("toc-title"), h.Str("On this page")), h.Ul(items...))
}

func (d *Doc) next() h.H {
	nx, ok := Next(d.nav.Path)
	if !ok {
		return nil
	}
	return h.P(h.Class("next"), h.A(h.Href(d.Href(nx.Path)), h.Str("Next: "+nx.Title)))
}

func (d *Doc) oldVersion() h.H {
	if d.site.Base == "" {
		return nil
	}
	cur, _ := d.site.Current()
	latest := d.site.Latest()
	text := "Read the latest"
	if latest.Label != "" {
		text += " (" + latest.Label + ")"
	}
	return h.P(h.Class("old-version"), h.Role("note"),
		h.Str("You are reading the docs for "+cmp.Or(cur.Label, strings.TrimPrefix(d.site.Base, "/"))+". "),
		h.A(h.Href(latest.Href(d.path())), h.Str(text)),
	)
}

// path is the page's own path, or the front page for one outside the nav.
func (d *Doc) path() string { return cmp.Or(d.nav.Path, "/") }
