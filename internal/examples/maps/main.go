// Maps is a server-driven world map: the camera, the markers, and a moving
// marker are all controlled from Go and pushed to the browser over SSE — no
// client JavaScript. City buttons fly the camera (FlyTo); a "drone" marker
// glides along a route, stepped by a server-side ticker (MoveMarker); the
// route line is a GeoJSON layer. Each tab runs its own ticker, so the motion
// is per-connection — this shows Go driving the map, not cross-tab fan-out
// (for that pattern, see the chat example's app-scoped state).
//
//	go run ./internal/examples/maps
//	open http://localhost:3000
package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/via/plugins/picocss"
)

type city struct {
	name     string
	lng, lat float64
	zoom     float64
}

var cities = []city{
	{"San Francisco", -122.42, 37.77, 9},
	{"New York", -74.0, 40.71, 9},
	{"London", -0.12, 51.5, 9},
	{"Tokyo", 139.69, 35.69, 9},
	{"Sydney", 151.21, -33.87, 9},
}

// route is the great-circle-ish path the drone walks, as [lng, lat] pairs.
var route = [][]float64{
	{-122.42, 37.77}, {-74.0, 40.71}, {-0.12, 51.5}, {139.69, 35.69}, {151.21, -33.87},
}

type Page struct {
	Map     *maplibre.Map
	Running via.SignalBool     `via:"running,init=true"`
	CityIdx via.SignalNum[int] `via:"cityIdx"` // which city a button click targets

	mu     sync.Mutex
	leg    int     // index of the current route segment start
	t      float64 // 0..1 progress along the current segment
	pins   int     // count of user-dropped pins, for unique marker ids
	ticker *via.Ticker
}

func (p *Page) OnInit(ctx *via.Ctx) error {
	if p.Map == nil {
		opts := []maplibre.MapOption{
			maplibre.WithElementID("map"),
			maplibre.WithCenter(maplibre.At(10, 30)),
			maplibre.WithZoom(1.4),
			maplibre.WithNavigationControl(),
			maplibre.WithScaleControl(maplibre.BottomLeft),
			// The route line is drawn from a GeoJSON source declared up front.
			maplibre.WithGeoJSONSource("route", maplibre.FeatureCollection(
				maplibre.Feature(maplibre.LineString(route), nil),
			)),
			maplibre.WithLayer(maplibre.LineLayer("route", "route",
				maplibre.Paint("line-color", "#ff9500"),
				maplibre.Paint("line-width", 2),
				maplibre.Layout("line-cap", "round"),
				maplibre.Layout("line-join", "round"),
			)),
			// A clickable "zone" polygon: a filled GeoJSON feature carrying an
			// id property. OnFeatureClick reports which zone was clicked.
			maplibre.WithGeoJSONSource("zones", maplibre.FeatureCollection(
				maplibre.Feature(
					maplibre.Polygon([][][]float64{{{20, 0}, {60, 0}, {60, 30}, {20, 30}, {20, 0}}}),
					map[string]any{"id": "zone-alpha", "name": "Operations zone Alpha"},
				),
			), maplibre.GenerateFeatureIDs()),
			maplibre.WithLayer(maplibre.FillLayer("zones", "zones",
				// Data-driven fill: amber while hovered, indigo otherwise.
				maplibre.Paint("fill-color", maplibre.WhenHovered("#ffcc00", "#5856d6")),
				maplibre.Paint("fill-opacity", 0.45),
			)),
			// Hover-to-highlight, entirely client-side (feature-state).
			maplibre.WithFeatureHover("zones"),
			// Click-to-place: a tap on the basemap round-trips into Go, which
			// drops a pin there — the inbound half of the map's interactivity.
			maplibre.OnClick(p.DropPin),
			// Marker gestures also round-trip: clicking a pin flies to it,
			// dragging one reports where it landed.
			maplibre.OnMarkerClick(p.FocusMarker),
			maplibre.OnMarkerDragEnd(p.PinMoved),
			// Clicking a rendered data-layer feature reports which one (by id).
			maplibre.OnFeatureClick("zones", p.ZoneClicked),
			// Escape hatch: right-click (contextmenu) reports the location +
			// live zoom — any MapLibre event can drive Go this way.
			maplibre.OnMapEvent("contextmenu", p.RightClicked),
		}
		// The cities are fixed pins — declare them at construction with
		// WithMarker, so they render on first paint with no OnConnect work.
		for _, c := range cities {
			opts = append(opts, maplibre.WithMarker(c.name, maplibre.At(c.lng, c.lat), maplibre.PopupText(c.name)))
		}
		p.Map = maplibre.NewMap(opts...)
	}
	return nil
}

func (p *Page) OnConnect(ctx *via.Ctx) error {
	// Only the drone is dynamic (a ticker moves it), so it's the one marker
	// added at connect time rather than declared with WithMarker.
	p.Map.AddMarker(ctx, "drone", maplibre.At(route[0][0], route[0][1]),
		maplibre.Color("#ff3b30"), maplibre.PopupText("Drone"))

	p.ticker = via.Stream(ctx, 80*time.Millisecond, func(ctx *via.Ctx, _ time.Time) {
		lng, lat := p.advance()
		p.Map.MoveMarker(ctx, "drone", maplibre.At(lng, lat))
	})
	return nil
}

// advance steps the drone along the route, looping back to the first leg after
// the last, and returns its new [lng, lat]. Guarded so a click and a tick can't
// race the leg/progress fields.
func (p *Page) advance() (float64, float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.t += 0.02
	if p.t >= 1 {
		p.t = 0
		p.leg = (p.leg + 1) % (len(route) - 1)
	}
	a, b := route[p.leg], route[p.leg+1]
	return a[0] + (b[0]-a[0])*p.t, a[1] + (b[1]-a[1])*p.t
}

func (p *Page) ToggleRunning(ctx *via.Ctx) {
	v := !p.Running.Read(ctx)
	p.Running.Write(ctx, v)
	if v {
		p.ticker.Resume()
	} else {
		p.ticker.Pause()
	}
}

// FlyToCity flies the camera to the city the clicked button selected via the
// cityIdx signal. on.Click takes a bound method, not a per-city closure, so
// the target rides in on a signal the button sets before the action fires.
func (p *Page) FlyToCity(ctx *via.Ctx) {
	idx := p.CityIdx.Read(ctx)
	if idx < 0 || idx >= len(cities) {
		return
	}
	c := cities[idx]
	p.Map.FlyTo(ctx, maplibre.At(c.lng, c.lat), c.zoom)
}

// DropPin places a green marker wherever the user clicked the basemap. The
// clicked [lng, lat] rides back in on the MapEvent — no client JavaScript.
func (p *Page) DropPin(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	p.mu.Lock()
	p.pins++
	n := p.pins
	p.mu.Unlock()
	p.Map.AddMarker(ctx, fmt.Sprintf("pin-%d", n), e.LngLat(),
		maplibre.Color("#34c759"),
		maplibre.Draggable(),
		// e.Zoom is the live camera at click time — carried on every gesture.
		maplibre.PopupText(fmt.Sprintf("Pin %d — %.4f, %.4f @ z%.1f", n, e.Lat, e.Lng, e.Zoom)))
}

// FocusMarker flies the camera to whichever marker the user clicked. The
// clicked marker's id and position ride back in on the MapEvent.
func (p *Page) FocusMarker(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	p.Map.FlyTo(ctx, e.LngLat(), 6)
	ctx.Toast(fmt.Sprintf("Selected %s", e.MarkerID))
}

// PinMoved reports where a dragged marker was dropped — drag-to-reposition,
// driven entirely from Go.
func (p *Page) PinMoved(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	ctx.Toast(fmt.Sprintf("%s moved to %.4f, %.4f", e.MarkerID, e.Lat, e.Lng))
}

// ZoneClicked opens a popup ("dialog") at the clicked feature, showing details
// the server looked up by the feature's id — the server-authoritative
// selection pattern, surfaced as an on-map dialog.
func (p *Page) ZoneClicked(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	p.Map.ShowPopup(ctx, "zone-info", e.LngLat(),
		fmt.Sprintf("Zone %s — click the map to dismiss", e.FeatureID),
		maplibre.PopupMaxWidth("220px"))
}

// RightClicked handles a contextmenu (right-click) via the OnMapEvent escape
// hatch, reporting where and at what zoom.
func (p *Page) RightClicked(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	ctx.Toast(fmt.Sprintf("Right-clicked %.3f, %.3f @ z%.1f", e.Lat, e.Lng, e.Zoom))
}

func (p *Page) View(ctx *via.CtxR) h.H {
	buttons := make([]h.H, len(cities))
	for i, c := range cities {
		buttons[i] = h.Button(h.Class("outline"), h.T(c.name),
			on.Click(p.FlyToCity, on.SetSignal(&p.CityIdx.Signal, i)))
	}
	return h.Body(
		h.Main(h.Class("container"),
			h.HGroup(
				h.H2(h.T("Via × MapLibre")),
				h.P(h.T("The camera, markers, and the moving drone are all driven from Go over SSE. Click anywhere on the map to drop a pin — the click round-trips through Go.")),
			),
			h.Div(append([]h.H{h.Class("grid")}, buttons...)...),
			h.P(
				h.Button(
					h.Data("text", "$running?'Pause drone':'Resume drone'"),
					on.Click(p.ToggleRunning),
				),
			),
			p.Map.Mount(),
		),
	)
}

func main() {
	app := via.New(
		via.WithTitle("Via × MapLibre"),
		via.WithPlugins(
			picocss.Plugin(picocss.WithDarkMode()),
			maplibre.Plugin(),
		),
	)
	via.Mount[Page](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
