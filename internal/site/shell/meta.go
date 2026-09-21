package shell

import "github.com/go-via/via"

// Meta takes the title from nav so a page names itself once. Assets are left
// zero: the site-wide ones are router-wide in main.go, and a page that loads
// its own sets the field itself (content/islands.go).
func Meta(nav NavItem, desc string) via.Meta {
	return via.Meta{Title: nav.Title + " · go-via", Description: desc}
}
