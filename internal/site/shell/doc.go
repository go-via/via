package shell

import (
	"cmp"
	"strconv"
	"strings"

	"github.com/go-via/via"
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
	// landing drops the contents list: the front page's few sections are
	// one scroll apart.
	landing bool
}

type heading struct {
	level     int
	id, title string
}

// Doc starts a render of the page nav names. search is the top bar's search
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
	first := len(d.heads) == 0
	d.heads = append(d.heads, heading{level: level, id: id, title: title})
	kids := []h.H{
		h.ID(id),
		h.A(h.Class("anchor"), h.Href("#"+id), h.Aria("label", "Link to this section"), h.Str("#")),
		h.Str(title),
	}
	el := h.H3(kids...)
	if level == 2 {
		el = h.H2(kids...)
	}
	if !first {
		return el
	}
	// The folded contents list goes after the lead, where a phone reader has
	// read what the page is about. It is rendered lazily, since the
	// headings after this one are not drawn yet.
	return via.Each([]h.H{via.When(true, d.inlineTOC), el}, same)
}

func same(n h.H) h.H { return n }

func (d *Doc) inlineTOC() h.H {
	if d.landing {
		return nil
	}
	inline, _ := d.toc()
	return inline
}

// Href is path on this build of the site.
func (d *Doc) Href(path string) string { return d.site.Href(path) }

// Page renders body under the page's heading in the site chrome. Go evaluates
// every argument, so every d.H2 in body, before the method runs: by then
// d.heads is complete and the contents list sees them all.
func (d *Doc) Page(body ...h.H) h.H {
	_, rail := d.toc()
	main := []h.H{h.ID("main"), h.Class("col"),
		d.oldVersion(), d.crumbs(), h.H1(h.Str(cmp.Or(d.nav.Heading, d.nav.Title)))}
	main = append(main, body...)
	next, hasNext := d.site.Next(d.nav.Path)
	return d.layout(main, d.foot(next, hasNext, rail != nil), rail)
}

// Landing is Page with a hero on top: art, heading, pitch, the Get started,
// Why Via? and GitHub links, then the body.
func (d *Doc) Landing(art h.H, pitch string, body ...h.H) h.H {
	why := NavFor("/why")
	hero := h.Section(h.Class("hero"), art,
		h.H1(h.Str(cmp.Or(d.nav.Heading, d.nav.Title))),
		h.P(h.Class("pitch"), h.Str(pitch)),
		h.Div(h.Class("hero-actions"),
			h.A(h.Class("btn btn-primary"), h.Href(d.Href("/start")), h.Str("Get started")),
			h.A(h.Class("btn"), h.Href(d.Href(why.Path)), h.Str("Why Via?")),
			ExtLink(repo, h.Class("btn"), h.Str("View on GitHub")),
		),
	)
	d.landing = true
	_, rail := d.toc()
	main := []h.H{h.ID("main"), h.Class("col"), d.oldVersion(), hero}
	main = append(main, body...)
	return d.layout(main, d.foot(why, true, rail != nil), nil)
}

func (d *Doc) crumbs() h.H {
	if d.nav.Group == "" || d.nav.Path == "/" {
		return nil
	}
	return h.Nav(h.Class("crumbs"), h.Aria("label", "Breadcrumb"),
		h.Str(d.nav.Group+" › "+d.nav.Title))
}

// toc is the contents list twice: folded into the column under 66.5rem, a
// sticky rail beside it from there (site.css shows one). Two copies because a
// <details> cannot be forced open by CSS at the wider width.
func (d *Doc) toc() (inline, rail h.H) {
	if len(d.heads) < 2 {
		return nil, nil
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
	inline = h.Details(h.Class("toc toc-inline"),
		h.Summary(h.Str("On this page")), h.Ul(items...))
	rail = h.Nav(h.Class("toc toc-rail"), h.Aria("label", "On this page"),
		h.P(h.Class("toc-title"), h.Str("On this page")), h.Ul(items...))
	return inline, rail
}

// foot is the pager and the page's own links. It renders after </main>, so
// search, which reads only <main>, never indexes "Next: …" as page text.
func (d *Doc) foot(next NavItem, hasNext, long bool) h.H {
	if d.nav.Path == "" {
		return nil
	}
	var pager []h.H
	if prev, ok := d.site.Prev(d.nav.Path); ok {
		pager = append(pager, h.A(h.Class("pager-prev"), h.Href(d.Href(prev.Path)), h.Rel("prev"),
			h.Small(h.Str("Previous")), h.Span(h.Str(prev.Title))))
	}
	if hasNext {
		pager = append(pager, h.A(h.Class("pager-next"), h.Href(d.Href(next.Path)), h.Rel("next"),
			h.Small(h.Str("Next")), h.Span(h.Str(next.Title))))
	}
	links := []h.H{h.Class("page-links"),
		ExtLink(repo+"/blob/main/internal/site/content/"+d.nav.SourceFile(), h.Str("Edit this page on GitHub"))}
	if long {
		links = append(links, h.A(h.Href("#top"), h.Class("to-top"), h.Str("Back to top")))
	}
	var nav h.H
	if len(pager) > 0 {
		nav = h.Nav(append([]h.H{h.Class("pager"), h.Aria("label", "Pages")}, pager...)...)
	}
	return h.Div(h.Class("page-foot"), nav, h.P(links...))
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
