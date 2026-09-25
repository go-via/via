package shell

import (
	"cmp"

	"github.com/go-via/via"
)

// Meta takes the title from nav so a page names itself once, and builds the
// canonical URL from origin — empty (local development) leaves it out rather
// than naming a host the page is not on. Declaring OG at all is what makes via
// fill og:title, og:description and og:url from the fields above. Assets are
// left zero: the site-wide ones are router-wide in site.New, and a page that
// loads its own sets the field itself (content/islands.go).
func Meta(nav NavItem, desc, origin string) via.Meta {
	// Heading, where it differs, is the page's full sentence — the front page
	// wants that in the tab, not "Home".
	title := cmp.Or(nav.Heading, nav.Title) + " · go-via"
	m := via.Meta{
		Title:       title,
		Description: desc,
		OG:          map[string]string{"type": "website"},
	}
	if origin != "" {
		m.Canonical = origin + nav.Path
	}
	return m
}
