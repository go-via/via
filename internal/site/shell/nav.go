package shell

import "github.com/go-via/via/h"

// h has no aria helper.
var ariaCurrentPage = h.RawAttr("aria-current", "page")

// NavItem is the one source of a page's title. The zero value is a page
// outside the nav.
type NavItem struct {
	Title   string
	Path    string
	Heading string // <h1> when it differs from Title
}

// Nav is the sidebar, in order, and the only place a page's path and title are
// spelled.
var Nav = []NavItem{
	{Title: "Home", Path: "/", Heading: "Server-rendered Go UI that stays live"},
	{Title: "Actions", Path: "/actions"},
	{Title: "Signals", Path: "/signals"},
	{Title: "Live", Path: "/live"},
	{Title: "Islands", Path: "/islands"},
	{Title: "Platform", Path: "/platform"},
	{Title: "Reference", Path: "/reference"},
}

// NavFor panics on a path with no entry: a mount the nav does not know about
// is a wiring mistake, not a runtime condition.
func NavFor(path string) NavItem {
	for _, it := range Nav {
		if it.Path == path {
			return it
		}
	}
	panic("shell: no nav entry for " + path)
}
