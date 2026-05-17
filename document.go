package via

import "github.com/go-via/via/h"

// AppendToHead adds nodes to the <head> of every rendered page. Call
// during boot (e.g. from a plugin's Register) — the underlying slice is
// not mutex-guarded, so concurrent appends after the server starts race
// with the page render path.
func (a *App) AppendToHead(elements ...h.H) {
	a.documentHeadIncludes = appendNonNil(a.documentHeadIncludes, elements)
}

// AppendToFoot adds nodes to the end of <body> on every rendered page.
// Same boot-time-only contract as AppendToHead.
func (a *App) AppendToFoot(elements ...h.H) {
	a.documentFootIncludes = appendNonNil(a.documentFootIncludes, elements)
}

// AppendAttrToHTML adds attributes to the <html> element of every page.
// Same boot-time-only contract as AppendToHead.
func (a *App) AppendAttrToHTML(attrs ...h.H) {
	a.documentHTMLAttrs = appendNonNil(a.documentHTMLAttrs, attrs)
}

// appendNonNil appends every non-nil element from src onto dst. Used by
// the document-mutation Append* helpers so they all share one nil-skip
// loop.
func appendNonNil(dst, src []h.H) []h.H {
	for _, n := range src {
		if n != nil {
			dst = append(dst, n)
		}
	}
	return dst
}
