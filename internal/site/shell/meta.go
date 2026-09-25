package shell

import (
	"cmp"

	"github.com/go-via/via"
)

// Meta takes the title from nav so a page names itself once, and builds the
// canonical URL from the site's origin — empty (local development) leaves it
// out rather than naming a host the page is not on. Declaring OG at all is
// what makes via fill og:title, og:description and og:url from the fields
// above. Assets are left zero: the site-wide ones are router-wide in site.New,
// and a page that loads its own sets the field itself (content/islands.go).
func Meta(s *Site, nav NavItem, desc string) via.Meta {
	// Heading, where it differs, is the page's full sentence — the front page
	// wants that in the tab, not "Home".
	title := cmp.Or(nav.Heading, nav.Title) + " · go-via"
	m := via.Meta{
		Title:       title,
		Description: desc,
		OG:          map[string]string{"type": "website"},
		Twitter:     map[string]string{"card": "summary_large_image"},
	}
	if s.Origin != "" {
		m.Canonical = s.Origin + s.Href(nav.Path)
		// The unfingerprinted URL: link unfurlers cache the image per URL,
		// and this one survives a redeploy.
		m.OG["image"] = s.Origin + s.Href("/static/brand/punch-dark.png")
	}
	if s.Base != "" {
		// An older version's pages would otherwise compete with the latest's
		// in search results.
		m.Robots = "noindex"
	}
	return m
}
