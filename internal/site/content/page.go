package content

import (
	"github.com/go-via/via"
	"go-via.dev/site/shell"
)

// page is the wiring every content page needs and nothing else: its nav entry,
// so the path is spelled once instead of in both PageMeta and View, and the
// origin its canonical URL is built from.
type page struct {
	nav    shell.NavItem
	origin string
}

func newPage(path, origin string) page {
	return page{nav: shell.NavFor(path), origin: origin}
}

func (p page) meta(desc string) via.Meta { return shell.Meta(p.nav, desc, p.origin) }
