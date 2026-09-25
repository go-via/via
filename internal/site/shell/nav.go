package shell

// NavItem is the one source of a page's title. The zero value is a page
// outside the nav.
type NavItem struct {
	Title   string
	Path    string
	Heading string // <h1> when it differs from Title
	// URL marks an entry that leaves the site: it is linked from the sidebar
	// and skipped by mounts, next links and the search index.
	URL string
	// Group is filled in by Pages, not spelled per item.
	Group string
}

// NavGroup is one titled run of sidebar entries.
type NavGroup struct {
	Title string
	Items []NavItem
}

// Nav is the sidebar, in order, and the only place the nav's paths and titles
// are spelled.
var Nav = []NavGroup{
	{Title: "Start", Items: []NavItem{
		{Title: "Overview", Path: "/", Heading: "Web UI in Go that stays live"},
		{Title: "Getting started", Path: "/start"},
	}},
	{Title: "Build", Items: []NavItem{
		{Title: "Actions", Path: "/actions"},
		{Title: "Signals", Path: "/signals"},
		{Title: "Live state", Path: "/live"},
		{Title: "Islands", Path: "/islands"},
	}},
	{Title: "Run", Items: []NavItem{
		{Title: "Sessions & security", Path: "/security"},
		{Title: "Deploy", Path: "/deploy"},
	}},
	{Title: "Reference", Items: []NavItem{
		{Title: "API", Path: "/reference", Heading: "API reference"},
		{Title: "Migrating from v0.7", Path: "/migrate"},
		{Title: "Changelog", URL: "https://github.com/go-via/via/releases"},
	}},
}

// NavFor panics on a path with no entry: a mount the nav does not know about
// is a wiring mistake, not a runtime condition.
func NavFor(path string) NavItem {
	for _, it := range Pages() {
		if it.Path == path {
			return it
		}
	}
	panic("shell: no nav entry for " + path)
}

// Pages is every entry the site serves itself, in nav order.
func Pages() []NavItem {
	var out []NavItem
	for _, g := range Nav {
		for _, it := range g.Items {
			if it.URL == "" {
				it.Group = g.Title
				out = append(out, it)
			}
		}
	}
	return out
}

// Next is the page after path in nav order; false on the last page and on a
// path outside the nav.
func Next(path string) (NavItem, bool) {
	pages := Pages()
	for i, it := range pages {
		if it.Path == path && i+1 < len(pages) {
			return pages[i+1], true
		}
	}
	return NavItem{}, false
}
