package via

import "github.com/go-via/via/h"

// AppendToHead adds nodes to the <head> of every rendered page. Call
// during boot (e.g. from a plugin's Register).
//
// Boot-only: panics if called after Start has bound the server. The
// read-side (page rendering) is lock-free for speed, so post-boot
// mutations would race with concurrent renders.
func (a *App) AppendToHead(elements ...h.H) {
	a.requireBoot("AppendToHead")
	a.documentHeadIncludes = appendNonNil(a.documentHeadIncludes, elements)
}

// AppendToFoot adds nodes to the end of <body> on every rendered page.
// Boot-only: panics if called after Start has bound the server.
func (a *App) AppendToFoot(elements ...h.H) {
	a.requireBoot("AppendToFoot")
	a.documentFootIncludes = appendNonNil(a.documentFootIncludes, elements)
}

// AppendAttrToHTML adds attributes to the <html> element of every page.
// Boot-only: panics if called after Start has bound the server.
func (a *App) AppendAttrToHTML(attrs ...h.H) {
	a.requireBoot("AppendAttrToHTML")
	a.documentHTMLAttrs = appendNonNil(a.documentHTMLAttrs, attrs)
}

// requireBoot panics if Start has already bound the server. Used by
// every boot-only mutator to surface the "configured too late" mistake
// at the call site rather than as a subtle race weeks later.
func (a *App) requireBoot(method string) {
	a.serverMu.Lock()
	started := a.server != nil
	a.serverMu.Unlock()
	if started {
		panic("via: App." + method + " called after Start; configure during boot")
	}
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
