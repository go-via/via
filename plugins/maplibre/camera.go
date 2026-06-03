package maplibre

import (
	"fmt"

	"github.com/go-via/via"
)

// FlyTo animates a curved, zoom-then-pan flight to at, at the given zoom — the
// "navigate the user somewhere" gesture. Server-driven: call it from an action
// and the camera flies on every connected tab.
func (m *Map) FlyTo(ctx *via.Ctx, at LngLat, zoom float64) {
	m.camera(ctx, "flyTo", at, zoom)
}

// EaseTo moves the camera to at, at the given zoom, with a straight eased
// transition — cheaper and less theatrical than [Map.FlyTo] for small hops.
func (m *Map) EaseTo(ctx *via.Ctx, at LngLat, zoom float64) {
	m.camera(ctx, "easeTo", at, zoom)
}

// JumpTo snaps the camera to at, at the given zoom, with no animation.
func (m *Map) JumpTo(ctx *via.Ctx, at LngLat, zoom float64) {
	m.camera(ctx, "jumpTo", at, zoom)
}

func (m *Map) camera(ctx *via.Ctx, method string, at LngLat, zoom float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.%s(%s)", m.mapRef(), method,
		mustJSON(map[string]any{"center": at.pair(), "zoom": zoom})))
}

// SetCenter recenters on at without changing zoom, instantly.
func (m *Map) SetCenter(ctx *via.Ctx, at LngLat) {
	ctx.ExecScript(fmt.Sprintf("%s?.setCenter(%s)", m.mapRef(), mustJSON(at.pair())))
}

// SetZoom sets the zoom level instantly.
func (m *Map) SetZoom(ctx *via.Ctx, zoom float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.setZoom(%s)", m.mapRef(), mustJSON(zoom)))
}

// SetPitch tilts the camera to deg degrees (0 = straight down).
func (m *Map) SetPitch(ctx *via.Ctx, deg float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.setPitch(%s)", m.mapRef(), mustJSON(deg)))
}

// SetBearing rotates the map to deg degrees clockwise from north.
func (m *Map) SetBearing(ctx *via.Ctx, deg float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.setBearing(%s)", m.mapRef(), mustJSON(deg)))
}

// FitBounds frames b in view, zooming and centering to fit. Use it to frame a
// route, a set of markers, or a region without computing the center/zoom
// yourself.
func (m *Map) FitBounds(ctx *via.Ctx, b Bounds) {
	bounds := [][]float64{b.sw(), b.ne()}
	ctx.ExecScript(fmt.Sprintf("%s?.fitBounds(%s,{padding:40})", m.mapRef(), mustJSON(bounds)))
}
