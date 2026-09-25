package shell

import "strings"

// NavItem is the one source of a page's title. The zero value is a page
// outside the nav.
type NavItem struct {
	Title   string
	Path    string
	Heading string // <h1> when it differs from Title
	// File is the page's source under content/ when it is not the path's
	// last segment plus .go; the edit link reads it through SourceFile.
	File string
	// URL marks an entry that leaves the site: it is linked from the sidebar
	// and skipped by mounts, next links and the search index.
	URL string
	// Group is filled in by Pages, not spelled per item.
	Group string
}

// NavGroup is one titled run of sidebar entries; an empty Title is a run of
// top-level entries with no label above them.
type NavGroup struct {
	Title string
	Items []NavItem
}

// Nav is the sidebar, in order, and the only place the nav's paths and titles
// are spelled.
var Nav = []NavGroup{
	{Items: []NavItem{
		{Title: "Home", Path: "/", Heading: "Web UI in Go that stays live", File: "landing.go"},
		{Title: "Why Via", Path: "/why"},
		{Title: "Examples", Path: "/examples"},
	}},
	{Title: "Learn", Items: []NavItem{
		{Title: "Getting started", Path: "/start"},
		{Title: "Tutorial: live chat", Path: "/tutorial"},
		{Title: "Actions", Path: "/actions"},
		{Title: "Signals", Path: "/signals"},
		{Title: "Live state", Path: "/live"},
		{Title: "Compositions", Path: "/compositions"},
	}},
	{Title: "Guides", Items: []NavItem{
		{Title: "Islands", Path: "/islands"},
		{Title: "Sessions & security", Path: "/security"},
		{Title: "Deploy", Path: "/deploy"},
		{Title: "Testing", Path: "/testing"},
		{Title: "Troubleshooting", Path: "/troubleshooting"},
	}},
	{Title: "Reference", Items: []NavItem{
		{Title: "API", Path: "/reference", Heading: "API reference"},
		{Title: "h helpers", Path: "/h"},
		{Title: "Glossary", Path: "/glossary"},
		{Title: "Migrating from v0.7", Path: "/migrate"},
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

// SourceFile is the page's file under internal/site/content.
func (it NavItem) SourceFile() string {
	if it.File != "" {
		return it.File
	}
	return strings.TrimPrefix(it.Path, "/") + ".go"
}

// Next is the page after path in nav order; false on the last one and on a
// path outside the nav.
func (s *Site) Next(path string) (NavItem, bool) { return s.step(path, 1) }

// Prev is the page before path in nav order.
func (s *Site) Prev(path string) (NavItem, bool) { return s.step(path, -1) }

func (s *Site) step(path string, dir int) (NavItem, bool) {
	pages := Pages()
	for i, it := range pages {
		if it.Path != path {
			continue
		}
		if j := i + dir; j >= 0 && j < len(pages) {
			return pages[j], true
		}
		break
	}
	return NavItem{}, false
}

// Suggest is the page whose path or title is closest to path by edit
// distance, for a 404's "did you mean"; false when nothing is close.
func Suggest(path string) (NavItem, bool) {
	q := strings.ToLower(strings.Trim(path, "/"))
	first, _, _ := strings.Cut(q, "/")
	var best NavItem
	bestD := -1
	for _, it := range Pages() {
		if it.Path == "/" {
			continue
		}
		for _, cand := range []string{strings.TrimPrefix(it.Path, "/"), Slug(it.Title)} {
			for _, in := range []string{q, first} {
				if d := distance(in, cand); bestD < 0 || d < bestD {
					best, bestD = it, d
				}
			}
		}
	}
	if bestD < 0 || bestD > max(2, len(first)/3) {
		return NavItem{}, false
	}
	return best, true
}

// distance is the Levenshtein distance over bytes; paths are ASCII.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
