package shell

import "github.com/go-via/via"

// NavItem is one sidebar entry. The zero value marks a page that is in no
// section of the nav, so nothing is highlighted.
type NavItem struct {
	Title string
	Path  string
}

// Nav is the sidebar, in reading order.
var Nav = []NavItem{
	{"Home", "/"},
	{"Actions", "/actions"},
	{"Signals", "/signals"},
	{"Live", "/live"},
	{"Islands", "/islands"},
	{"Platform", "/platform"},
	{"Reference", "/reference"},
}

// Meta fills the document head slots a content page owns. Assets are left
// zero: the site-wide ones are declared router-wide in main.go, and a page
// that loads its own sets the field itself (content/islands.go).
func Meta(title, desc string) via.Meta {
	return via.Meta{Title: title + " · go-via", Description: desc}
}
