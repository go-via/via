// Package icon is the site's inline SVG icons. Inline, not an icon font or a
// sprite file: no extra request, and stroke colour follows the text through
// currentColor. Stroke and fill are set in site.css (.icon), since style-src
// 'self' rules out style attributes.
package icon

import "github.com/go-via/via/h"

func svg(class string, paths ...string) h.H {
	kids := make([]h.H, 0, len(paths)+3)
	kids = append(kids, h.Class("icon "+class), h.RawAttr("viewBox", "0 0 24 24"), h.Aria("hidden", "true"))
	for _, d := range paths {
		kids = append(kids, h.El("path", h.RawAttr("d", d)))
	}
	return h.El("svg", kids...)
}

// Search is a magnifier.
func Search() h.H { return svg("icon-search", "M11 3a8 8 0 1 0 0 16a8 8 0 1 0 0-16", "m21 21-4.3-4.3") }

// External marks a link that opens another site in a new tab.
func External() h.H {
	return svg("icon-ext", "M15 3h6v6", "M10 14 21 3", "M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6")
}

// Menu is a hamburger.
func Menu() h.H { return svg("icon-menu", "M4 6h16", "M4 12h16", "M4 18h16") }

// Close is an ×.
func Close() h.H { return svg("icon-close", "M18 6 6 18", "M6 6l12 12") }

// Clipboard is the copy button's resting state.
func Clipboard() h.H {
	return svg("icon-clip",
		"M9 2h6a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1z",
		"M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2")
}

// Check is the copy button's done state.
func Check() h.H { return svg("icon-check", "M20 6 9 17l-5-5") }
