package demos

import (
	"math/rand/v2"
	"strconv"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
)

// MapView is what one map reads: the JSON maplibre's jumpTo wants, spelled in
// Go. Center is [longitude, latitude]. Name is json:"-" because it is for the
// container's aria-label and not for the library.
type MapView struct {
	Center [2]float64 `json:"center"`
	Zoom   float64    `json:"zoom"`
	Name   string     `json:"-"`
}

var cities = []MapView{
	{[2]float64{-9.14, 38.72}, 4, "Lisbon"},
	{[2]float64{139.69, 35.69}, 4, "Tokyo"},
	{[2]float64{-43.20, -22.91}, 3, "Rio de Janeiro"},
	{[2]float64{18.42, -33.92}, 3, "Cape Town"},
	{[2]float64{-74.01, 40.71}, 4, "New York"},
	{[2]float64{151.21, -33.87}, 3, "Sydney"},
	{[2]float64{28.98, 41.01}, 4, "Istanbul"},
	{[2]float64{77.21, 28.61}, 3, "Delhi"},
}

// maxMaps is the cap the grid is written to. One Signal per map, as fixed
// fields: the number of maps changes while the page is open, and a Signal
// reached through a slice has no field name to spell in a Datastar
// expression. A slice of via.Child would have the same problem one level up —
// a child's key is its position among its parent's Child calls and must not
// move for the life of the connection.
const maxMaps = 6

// MapGrid is one live unit holding every map, not a parent with a child per
// map: only the unit a Signal is a field of can Set it onto the wire, so the
// buttons and the six views live together.
type MapGrid struct {
	N                      via.State[int]      `via:"init=2"`
	V0, V1, V2, V3, V4, V5 via.Signal[MapView] `via:"init={\"center\":[0,20],\"zoom\":0}"`
}

func (g *MapGrid) views() []*via.Signal[MapView] {
	return []*via.Signal[MapView]{&g.V0, &g.V1, &g.V2, &g.V3, &g.V4, &g.V5}
}

func (g *MapGrid) OnInit(ctx *via.Ctx) error {
	for i, v := range g.views()[:g.N.Get()] {
		v.Set(cities[i])
	}
	return nil
}

func (g *MapGrid) Add(ctx *via.Ctx) {
	n := g.N.Get()
	if n >= maxMaps {
		return
	}
	// The signal first: the patch-signals frame goes out before the element
	// patch, so the new container's effect runs against a view, not the seed.
	g.views()[n].Set(cities[rand.IntN(len(cities))])
	g.N.Set(n + 1)
}

func (g *MapGrid) Remove(ctx *via.Ctx) {
	if n := g.N.Get(); n > 1 {
		g.N.Set(n - 1)
	}
}

// Shuffle moves every map without rebuilding any of them: the effect re-runs
// on the new signal value and takes the jumpTo branch.
func (g *MapGrid) Shuffle(ctx *via.Ctx) {
	for _, v := range g.views()[:g.N.Get()] {
		v.Set(cities[rand.IntN(len(cities))])
	}
}

func (g *MapGrid) View() h.H {
	grid := []h.H{h.Class("map-grid")}
	for i, v := range g.views()[:g.N.Get()] {
		// A stable id so a removal patches away the map that left, not the
		// last one; DataIgnoreMorph so the patch leaves MapLibre's own DOM
		// alone.
		// MapLibre injects a focusable canvas, so the group is the screen
		// reader's handle; outside the ignore-morph container, its label
		// follows the shuffle.
		grid = append(grid, h.Div(h.Class("map-slot"), h.Role("group"),
			h.RawAttr("aria-label", "Map of "+v.Get().Name),
			h.Div(h.Class("map"), h.ID("map-"+strconv.Itoa(i)),
				h.Data("island-map", ""), h.DataIgnoreMorph(),
				h.DataEffect(expr.Call("viaMap", expr.El, v.Ref())))))
	}
	return h.Div(
		h.Div(h.Class("row"),
			h.Button(via.On("click", g.Add), h.Str("Add map")),
			h.Button(via.On("click", g.Remove), h.Str("Remove last")),
			h.Button(via.On("click", g.Shuffle), h.Str("Shuffle")),
			h.Small(g.N.Display(), h.Str(" of "), h.Str(maxMaps)),
		),
		h.Div(grid...),
	)
}
