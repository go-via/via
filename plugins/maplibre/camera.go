package maplibre

import (
	"fmt"

	"github.com/go-via/via"
)

// FlyTo animates a curved, zoom-then-pan flight to (lng, lat) at zoom — the
// "navigate the user somewhere" gesture. Server-driven: call it from an
// action and the camera flies on every connected tab. Coordinates are
// [lng, lat] (longitude first).
func (m *Map) FlyTo(ctx *via.Ctx, lng, lat, zoom float64) {
	m.camera(ctx, "flyTo", lng, lat, zoom)
}

// EaseTo moves the camera to (lng, lat) at zoom with a straight eased
// transition — cheaper and less theatrical than [Map.FlyTo] for small hops.
func (m *Map) EaseTo(ctx *via.Ctx, lng, lat, zoom float64) {
	m.camera(ctx, "easeTo", lng, lat, zoom)
}

// JumpTo snaps the camera to (lng, lat) at zoom with no animation.
func (m *Map) JumpTo(ctx *via.Ctx, lng, lat, zoom float64) {
	m.camera(ctx, "jumpTo", lng, lat, zoom)
}

func (m *Map) camera(ctx *via.Ctx, method string, lng, lat, zoom float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.%s(%s)", m.mapRef(), method,
		mustJSON(map[string]any{"center": []float64{lng, lat}, "zoom": zoom})))
}

// SetCenter recenters on (lng, lat) without changing zoom, instantly.
func (m *Map) SetCenter(ctx *via.Ctx, lng, lat float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.setCenter(%s)", m.mapRef(), mustJSON([]float64{lng, lat})))
}

// SetZoom sets the zoom level instantly.
func (m *Map) SetZoom(ctx *via.Ctx, zoom float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.setZoom(%g)", m.mapRef(), zoom))
}

// SetPitch tilts the camera to deg degrees (0 = straight down).
func (m *Map) SetPitch(ctx *via.Ctx, deg float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.setPitch(%g)", m.mapRef(), deg))
}

// SetBearing rotates the map to deg degrees clockwise from north.
func (m *Map) SetBearing(ctx *via.Ctx, deg float64) {
	ctx.ExecScript(fmt.Sprintf("%s?.setBearing(%g)", m.mapRef(), deg))
}

// FitBounds frames the box (west, south, east, north) in view, zooming and
// centering to fit. Use it to frame a route, a set of markers, or a region
// without computing the center/zoom yourself.
func (m *Map) FitBounds(ctx *via.Ctx, west, south, east, north float64) {
	bounds := [][]float64{{west, south}, {east, north}}
	ctx.ExecScript(fmt.Sprintf("%s?.fitBounds(%s,{padding:40})", m.mapRef(), mustJSON(bounds)))
}
