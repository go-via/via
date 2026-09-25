package search

import (
	"html"
	"regexp"
	"strings"
)

var (
	mainEl   = regexp.MustCompile(`(?s)<main[^>]*>(.*)</main>`)
	dropped  = regexp.MustCompile(`(?s)<nav[\s>].*?</nav>|<pre[\s>].*?</pre>|<details[\s>].*?</details>|<button[\s>].*?</button>`)
	headings = regexp.MustCompile(`(?s)<h([23]) id="([^"]+)">(.*?)</h[23]>`)
	tags     = regexp.MustCompile(`(?s)<[^>]*>`)
	rows     = regexp.MustCompile(`(?s)<tr([^>]*)>\s*<td[^>]*>\s*(<code.*?)</td>(.*?)</tr>`)
	rowID    = regexp.MustCompile(`\bid="([^"]+)"`)
	noindex  = regexp.MustCompile(`<meta name="robots" content="[^"]*\bnoindex\b`)
	spaces   = regexp.MustCompile(`\s+`)
)

// Extract turns one rendered page into a Doc: the <main> element's text, cut
// at every h2 and h3 with an id. Code blocks, the demos' folded panes,
// button labels and every nav (the contents list, the pager) are left out:
// the first three would match every query about syntax, the navs repeat other
// pages' titles. A table row whose first cell is code, as on /reference, is a
// section of its own, headed by that cell and linked by the row's id, else by
// the heading above it.
func Extract(title, href, page string) Doc {
	d := Doc{Title: title, Href: href, NoIndex: noindex.MatchString(page)}
	m := mainEl.FindStringSubmatch(page)
	if m == nil {
		return d
	}
	body := dropped.ReplaceAllString(m[1], " ")
	cur := Section{}
	last := 0
	for _, loc := range headings.FindAllStringSubmatchIndex(body, -1) {
		d.Sections = appendSection(d.Sections, cur, body[last:loc[0]])
		heading := strings.TrimSpace(strings.TrimPrefix(plain(body[loc[6]:loc[7]]), "#"))
		cur = Section{Heading: heading, Anchor: body[loc[4]:loc[5]]}
		last = loc[1]
	}
	d.Sections = appendSection(d.Sections, cur, body[last:])
	return d
}

// appendSection adds cur with markup as its text, then markup's code rows.
func appendSection(out []Section, cur Section, markup string) []Section {
	var own []Section
	markup = rows.ReplaceAllStringFunc(markup, func(tr string) string {
		m := rows.FindStringSubmatch(tr)
		r := Section{Heading: plain(m[2]), Anchor: cur.Anchor, Text: plain(m[3])}
		if id := rowID.FindStringSubmatch(m[1]); id != nil {
			r.Anchor = html.UnescapeString(id[1])
		}
		own = append(own, r)
		return " "
	})
	cur.Text = plain(markup)
	if cur.Anchor != "" || cur.Text != "" {
		out = append(out, cur)
	}
	return append(out, own...)
}

func plain(s string) string {
	s = html.UnescapeString(tags.ReplaceAllString(s, " "))
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}
