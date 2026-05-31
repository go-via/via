// Package maplibre provides a MapLibre GL JS plugin for the Via engine —
// interactive vector maps driven from Go, with the camera, markers, and data
// layers updated over SSE.
//
// Quick start:
//
//	app := via.New(via.WithPlugins(maplibre.Plugin()))
//
// Hold a *Map on the page, build it in OnInit, mount it in View, then drive it
// from actions or a via.Stream ticker:
//
//	type Page struct{ Map *maplibre.Map }
//
//	func (p *Page) OnInit(ctx *via.Ctx) error {
//	    if p.Map == nil {
//	        p.Map = maplibre.NewMap(
//	            maplibre.WithElementID("map"),
//	            maplibre.WithCenter(-122.42, 37.77), // [lng, lat]
//	            maplibre.WithZoom(11),
//	            maplibre.WithNavigationControl(),
//	        )
//	    }
//	    return nil
//	}
//
//	func (p *Page) View(ctx *via.CtxR) h.H { return p.Map.Mount() }
//
//	func (p *Page) GoToTokyo(ctx *via.Ctx) { p.Map.FlyTo(ctx, 139.69, 35.69, 10) }
//
// # Coordinate order
//
// MapLibre — and this package — take coordinates as [lng, lat] (longitude
// first), the inverse of the lat/lng most map UIs print. WithCenter, FlyTo,
// AddMarker, and every GeoJSON coordinate follow this order.
//
// # Camera
//
// FlyTo (curved flight), EaseTo (eased hop), JumpTo (instant), plus SetCenter,
// SetZoom, SetPitch, SetBearing, and FitBounds(w, s, e, n) to frame a box.
//
// # Markers
//
// AddMarker(ctx, id, lng, lat, opts…) places a keyed marker; MoveMarker
// repositions it live (vehicle tracking), RemoveMarker / ClearMarkers tear
// down. Options: Color, Draggable, Scale, PopupText (XSS-safe, for user
// content), PopupHTML (trusted markup only — MapLibre does not sanitize it).
//
// # Data layers
//
// Declare sources/layers at construction with WithGeoJSONSource + WithLayer,
// or add them at runtime with AddGeoJSONSource / AddLayer. Push live data with
// SetGeoJSON(ctx, sourceID, fc). Build GeoJSON with Point, LineString,
// Polygon, Feature, PointFeature, FeatureCollection; build layers with
// CircleLayer / LineLayer / FillLayer / SymbolLayer plus Paint / Layout /
// Filter options. SetPaintProperty, SetLayerVisibility, RemoveLayer, and
// RemoveLayerAndSource adjust them after the fact. These ops re-arm on the
// style-load event, so they're safe to fire before the style finishes loading.
//
// # Lifecycle and escape hatch
//
//   - SetStyle swaps the basemap; Resize recomputes layout; Dispose frees the
//     WebGL context and registry slot (call on SPA unmount).
//   - Call(ctx, method, args…) invokes any Map method the typed API misses.
//   - WithMapOption(key, value) sets any constructor option not covered.
//
// # Self-hosting, versions, and CSP
//
//	maplibre.Plugin(maplibre.WithVersion("5.24.0")) // pin a CDN version (v5 only)
//	maplibre.Plugin(maplibre.WithSource("/static/maplibre-gl.js"),
//	    maplibre.WithStylesheet("/static/maplibre-gl.css")) // self-host
//	maplibre.Plugin(maplibre.WithCSPBuild()) // inline worker for strict worker-src
//
// Pin a v5 release — v6 is ESM-only and drops the maplibregl global the script
// include relies on. The CSS is required: without it markers, popups, and
// controls render unstyled. The default style is MapLibre's no-key demo style,
// intended for demos and CI, not production; supply your own via WithStyle.
package maplibre
