package content

import (
	"github.com/go-via/via"
	"go-via.dev/site/search"
	"go-via.dev/site/shell"
)

// Env is what every page is built from: where the site is served, and the
// index its search box reads.
type Env struct {
	Site  *shell.Site
	Index *search.Index
}

// page is the wiring every content page needs and nothing else: its nav entry,
// so the path is spelled once instead of in both PageMeta and View, the site
// its links and canonical URL are built from, and the sidebar's search box.
type page struct {
	nav    shell.NavItem
	site   *shell.Site
	Search search.Box
}

func newPage(path string, env Env) page {
	return page{nav: shell.NavFor(path), site: env.Site, Search: search.NewBox(env.Index)}
}

func (p *page) doc() *shell.Doc { return p.site.Doc(p.nav, via.Child(p.Search)) }

func (p *page) meta(desc string) via.Meta { return shell.Meta(p.site, p.nav, desc) }
