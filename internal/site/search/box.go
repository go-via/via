package search

import (
	"strconv"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"go-via.dev/site/icon"
)

// Box is the top bar's search field and its results, as an ARIA combobox:
// "/" or Ctrl/Cmd-K focuses it, the arrows move through the hits, Enter opens
// one and Escape clears the query.
type Box struct {
	idx   *Index
	Query via.Signal[string]
	// Count is a signal so the input's attributes can follow it: the input
	// itself is never morphed.
	Count  via.Signal[int]   `via:"init=0"`
	Active via.SignalCS[int] `via:"init=-1"`
	hits   []Hit
	asked  bool
}

const (
	inputID   = "q"
	listID    = "q-hits"
	optionID  = "q-hit-"
	maxHits   = 8
	minLength = 2
)

// NewBox queries idx, which may still be filling: nothing is served until
// site.New has added every page.
func NewBox(idx *Index) Box { return Box{idx: idx} }

// Find runs the query the input posted.
func (b *Box) Find(ctx *via.Ctx) {
	q := b.Query.Get()
	b.hits = b.idx.Find(q, maxHits)
	b.asked = len([]rune(q)) >= minLength
	b.Count.Set(len(b.hits))
}

func (b *Box) View() h.H {
	open := expr.Rawf("%s.length >= %s && %s > 0", b.Query.Ref(), expr.Val(minLength), b.Count.Ref())
	return h.Search(h.Class("search"),
		on.KeydownCS(focusKeys(), on.Window()),
		h.Label(h.Class("sr-only"), h.For(inputID), h.Str("Search the docs")),
		icon.Search(),
		h.Input(h.ID(inputID), h.Type("search"), h.Placeholder("Search Via"), h.AutoComplete("off"),
			h.Role("combobox"), h.Aria("autocomplete", "list"), h.Aria("controls", listID),
			h.Aria("expanded", "false"),
			h.DataAttr("aria-expanded", expr.Rawf("String(%s)", open)),
			h.DataAttr("aria-activedescendant", expr.Rawf("%s >= 0 ? %s + %s : ''",
				b.Active.Ref(), expr.Val(optionID), b.Active.Ref())),
			b.Query.Bind(),
			on.Input(b.Find, on.Debounce(250*time.Millisecond)),
			on.KeydownCS(b.keys()),
			// The response re-renders this region; morphing the input would
			// drop whatever was typed after the request left.
			h.DataIgnoreMorph(),
		),
		h.Kbd(h.Class("search-key"), h.Aria("hidden", "true"), h.Str("/")),
		h.P(h.Class("sr-only"), h.Aria("live", "polite"), h.Str(b.status())),
		b.results(),
		via.When(b.asked && len(b.hits) == 0, b.none),
	)
}

// focusKeys leaves "/" alone while another field has the caret, so it can
// still be typed there.
func focusKeys() expr.Expr {
	return expr.Rawf(`((evt.key === '/' && !evt.target.closest?.('input, textarea, select, [contenteditable]')) || `+
		`((evt.ctrlKey || evt.metaKey) && evt.key === 'k')) && (evt.preventDefault(), document.getElementById(%s).focus())`,
		expr.Val(inputID))
}

// keys runs on the input. Any key but the four it handles unsets the active
// hit, since typing is about to replace the list.
func (b *Box) keys() expr.Expr {
	a, q, n := b.Active.Ref(), b.Query.Ref(), b.Count.Ref()
	return expr.Rawf(`evt.key === 'ArrowDown' ? (evt.preventDefault(), %s = Math.min(%s + 1, %s - 1)) : `+
		`evt.key === 'ArrowUp' ? (evt.preventDefault(), %s = Math.max(%s - 1, -1)) : `+
		`evt.key === 'Enter' ? (evt.preventDefault(), document.getElementById(%s + Math.max(%s, 0))?.querySelector('a')?.click()) : `+
		`evt.key === 'Escape' ? (%s !== '' ? (%s = '', %s = -1) : el.blur()) : `+
		`(%s = -1)`,
		a, a, n,
		a, a,
		expr.Val(optionID), a,
		q, q, a,
		a)
}

func (b *Box) status() string {
	switch {
	case !b.asked:
		return ""
	case len(b.hits) == 0:
		return "No results"
	case len(b.hits) == 1:
		return "1 result"
	default:
		return strconv.Itoa(len(b.hits)) + " results"
	}
}

// Escape clears the query in the browser only, so the stale results hide off
// the signal rather than waiting for a request.
func (b *Box) shown() expr.Expr { return b.Query.Ref().Ne("") }

// The listbox is rendered even when empty, so aria-controls always names an
// element.
func (b *Box) results() h.H {
	items := make([]h.H, 0, len(b.hits)+5)
	items = append(items, h.ID(listID), h.Class("search-hits"), h.Role("listbox"),
		h.Aria("label", "Results"), h.DataShow(expr.All(b.shown(), b.Count.Ref().Gt(0))))
	for i, hit := range b.hits {
		label := hit.Title
		if hit.Heading != "" {
			label += " › " + hit.Heading
		}
		is := b.Active.Ref().Eq(i)
		items = append(items, h.Li(h.ID(optionID+strconv.Itoa(i)), h.Role("option"),
			h.Aria("selected", "false"), h.DataAttr("aria-selected", expr.Rawf("String(%s)", is)),
			h.DataClass("active", is),
			// The option, not the link, is what the arrows move to; a tab
			// stop per hit would put eight between the input and the page.
			h.A(h.Href(hit.Href), h.TabIndex(-1), h.Str(label)), h.Small(h.Str(hit.Snippet))))
	}
	return h.Ul(items...)
}

func (b *Box) none() h.H {
	return h.P(h.Class("search-none"), h.DataShow(b.shown()), h.Str("No matches"))
}
