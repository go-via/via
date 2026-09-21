package shell

import (
	"cmp"

	"github.com/go-via/via"
)

// Meta takes the title from nav so a page names itself once, and builds the
// canonical and Open Graph URLs from origin — empty (local development) leaves
// them out rather than naming a host the page is not on. Assets are left zero:
// the site-wide ones are router-wide in site.New, and a page that loads its own
// sets the field itself (content/islands.go).
func Meta(nav NavItem, desc, origin string) via.Meta {
	// Heading, where it differs, is the page's full sentence — the front page
	// wants that in the tab, not "Home".
	title := cmp.Or(nav.Heading, nav.Title) + " · go-via"
	m := via.Meta{
		Title:       title,
		Description: desc,
		OG: map[string]string{
			"title":       title,
			"description": desc,
			"type":        "website",
		},
	}
	if origin != "" {
		m.Canonical = origin + nav.Path
		m.OG["url"] = m.Canonical
	}
	return m
}
