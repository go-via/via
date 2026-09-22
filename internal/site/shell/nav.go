package shell

// NavItem is the one source of a page's title. The zero value is a page
// outside the nav.
type NavItem struct {
	Title   string
	Path    string
	Heading string // <h1> when it differs from Title
}

// Nav is the sidebar, in order, and the only place the nav's paths and titles
// are spelled; the next-page link at the foot of each page spells its own.
var Nav = []NavItem{
	{Title: "Home", Path: "/", Heading: "Web UI in Go that stays live"},
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
