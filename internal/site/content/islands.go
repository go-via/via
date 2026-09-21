package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/shell"
)

// islandAssets is a package var, not a literal built in PageMeta: Assets
// decides the mount's CSP and via reads it at Mount, so it has to be a
// constant of the type.
var islandAssets = via.Assets{
	Scripts: []via.Script{
		{Src: "/static/vendor/maplibre-gl-csp.js", Defer: true},
		{Src: "/static/islands.js", Defer: true},
	},
	Styles: []via.Style{{Href: "/static/islands.css"}},
}

// Islands is the page on handing a subtree to a JS library.
type Islands struct {
	page
	Spark demos.Sparkline
	Maps  demos.MapGrid
	Chart demos.Chart
}

// NewIslands builds the islands page for a deployment at origin.
func NewIslands(origin string) Islands { return Islands{page: newPage("/islands", origin)} }

func (p *Islands) PageMeta() via.Meta {
	m := p.meta("Handing a subtree to a JS library: DataIgnoreMorph, DataEffect and a teardown that leaves nothing behind.")
	m.Assets = islandAssets
	return m
}

func (p *Islands) View() h.H {
	return shell.Page(p.nav,
		h.P(
			h.Str("via never generates JavaScript. An island is what it offers instead: a container via renders once and then leaves alone. "),
			h.Code(h.Str("h.DataIgnoreMorph()")),
			h.Str(" keeps a live patch out of the subtree the library owns, "),
			h.Code(h.Str("h.DataEffect(…)")),
			h.Str(" names a function of yours and the signals it reads, and Go moves the island by writing those signals. "),
			h.Str("The script is yours; via only decides when it runs."),
		),

		demo.Card("Sparkline on a clock",
			h.P(
				h.Str("A "),
				h.Code(h.Str("Tick")),
				h.Str(" appends a sample and Sets a "),
				h.Code(h.Str("Signal[[]int]")),
				h.Str(" the View never binds or displays. Set is what declares the signal, so the value reaches the browser anyway. "),
				h.Str("The canvas is drawn by viaChart in static/islands.js, which the effect re-runs on every change."),
			),
			via.Child(p.Spark), "sparkline.go"),

		demo.Card("Six maps, one script",
			h.P(
				h.Str("Add, remove and shuffle MapLibre instances; the script tag is declared once, in the page's "),
				h.Code(h.Str("PageMeta().Assets")),
				h.Str(", and serves however many maps the grid holds. "),
				h.Str("A data-effect re-runs on every change of every signal it reads, so init has to be idempotent. "),
				h.Str("viaMap builds the map only when "),
				h.Code(h.Str("el._map")),
				h.Str(" is unset and otherwise only calls jumpTo, which is why Shuffle jumps the maps instead of rebuilding them. "),
				h.Str("Client-side teardown is yours: via has no client-side unmount hook, so islands.js keeps one MutationObserver on the document and calls "),
				h.Code(h.Str("_map.remove()")),
				h.Str(" for every island a patch took out of the DOM. "),
				h.Str("The server side has one: ctx.OnDispose runs when the unit's connection closes, which is where a producer feeding the island is stopped. "),
				h.Str("Everything here is same-origin — MapLibre's CSP build with its worker served from /static/vendor/, one GeoJSON file, and a style with no tiles, glyphs or sprite — so the page's "),
				h.Code(h.Str("default-src 'self'")),
				h.Str(" needs no widening."),
			),
			via.Child(p.Maps), "mapgrid.go"),

		demo.Card("The same island, no clock",
			h.P(
				h.Str("The same viaChart, driven by a click. This demo holds no State and never ticks, so the page does not stream for it: "),
				h.Str("the action's element patch carries the one signal the handler wrote, and the effect re-runs on arrival."),
			),
			via.Child(p.Chart), "chart.go"),

		h.P(h.A(h.Href("/platform"), h.Str("Next: composition, auth and the security floor"))),
	)
}
