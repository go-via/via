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
	ticker *via.Ticker
}

func (p *Page) OnInit(ctx *via.Ctx) error {
	if p.Map == nil {
		p.Map = maplibre.NewMap(
			maplibre.WithElementID("map"),
			maplibre.WithCenter(10, 30),
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
		)
	}
	return nil
}

func (p *Page) OnConnect(ctx *via.Ctx) error {
	for _, c := range cities {
		p.Map.AddMarker(ctx, c.name, c.lng, c.lat, maplibre.PopupText(c.name))
	}
	p.Map.AddMarker(ctx, "drone", route[0][0], route[0][1],
		maplibre.Color("#ff3b30"), maplibre.PopupText("Drone"))

	p.ticker = via.Stream(ctx, 80*time.Millisecond, func(ctx *via.Ctx, _ time.Time) {
		lng, lat := p.advance()
		p.Map.MoveMarker(ctx, "drone", lng, lat)
	})
	return nil
}

// advance steps the drone along the route, ping-ponging at the ends, and
// returns its new [lng, lat]. Guarded so a click and a tick can't race the
// leg/progress fields.
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
	p.Map.FlyTo(ctx, c.lng, c.lat, c.zoom)
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
				h.P(h.T("The camera, markers, and the moving drone are all driven from Go over SSE.")),
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
