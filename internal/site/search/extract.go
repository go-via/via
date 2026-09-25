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
	spaces   = regexp.MustCompile(`\s+`)
)

// Extract turns one rendered page into a Doc: the <main> element's text, cut
// at every h2 and h3 with an id. Code blocks, the demos' folded panes,
// button labels and the contents list are left out: the first three would
// match every query about syntax, the last repeats the headings.
func Extract(title, href, page string) Doc {
	d := Doc{Title: title, Href: href}
	m := mainEl.FindStringSubmatch(page)
	if m == nil {
		return d
	}
	body := dropped.ReplaceAllString(m[1], " ")
	cur := Section{}
	last := 0
	for _, loc := range headings.FindAllStringSubmatchIndex(body, -1) {
		cur.Text = plain(body[last:loc[0]])
		if cur.Anchor != "" || cur.Text != "" {
			d.Sections = append(d.Sections, cur)
		}
		heading := strings.TrimSpace(strings.TrimPrefix(plain(body[loc[6]:loc[7]]), "#"))
		cur = Section{Heading: heading, Anchor: body[loc[4]:loc[5]]}
		last = loc[1]
	}
	cur.Text = plain(body[last:])
	if cur.Anchor != "" || cur.Text != "" {
		d.Sections = append(d.Sections, cur)
	}
	return d
}

func plain(s string) string {
	s = html.UnescapeString(tags.ReplaceAllString(s, " "))
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}
