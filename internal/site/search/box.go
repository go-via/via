package search

import (
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// Box is the sidebar's search field and its results.
type Box struct {
	idx   *Index
	Query via.Signal[string]
	hits  []Hit
	asked bool
}

// NewBox queries idx, which may still be filling: nothing is served until
// site.New has added every page.
func NewBox(idx *Index) Box { return Box{idx: idx} }

// Find runs the query the input posted.
func (b *Box) Find(ctx *via.Ctx) {
	q := b.Query.Get()
	b.hits = b.idx.Find(q, 8)
	b.asked = len([]rune(q)) >= 2
}

func (b *Box) View() h.H {
	return h.Search(h.Class("search"),
		h.Label(h.Class("sr-only"), h.For("q"), h.Str("Search the docs")),
		h.Input(h.ID("q"), h.Type("search"), h.Placeholder("Search"), h.AutoComplete("off"),
			b.Query.Bind(),
			on.Input(b.Find, on.Debounce(250*time.Millisecond)),
			on.KeydownCS(b.escape()),
			// The response re-renders this region; morphing the input would
			// drop whatever was typed after the request left.
			h.DataIgnoreMorph(),
		),
		via.When(b.asked, b.results),
	)
}

// evt has no expr constructor, hence the Raw.
func (b *Box) escape() expr.Expr {
	return expr.All(expr.Raw("evt.key").Eq("Escape"), b.Query.Ref().Assign(""))
}

func (b *Box) results() h.H {
	// Escape clears the query in the browser only, so the stale results hide
	// off the signal rather than waiting for a request.
	show := h.DataShow(b.Query.Ref().Ne(""))
	if len(b.hits) == 0 {
		return h.P(h.Class("search-none"), show, h.Str("No matches"))
	}
	items := make([]h.H, 0, len(b.hits)+2)
	items = append(items, h.Class("search-hits"), show)
	for _, hit := range b.hits {
		label := hit.Title
		if hit.Heading != "" {
			label += " › " + hit.Heading
		}
		items = append(items, h.Li(h.A(h.Href(hit.Href), h.Str(label)), h.Small(h.Str(hit.Snippet))))
	}
	return h.Ul(items...)
}
