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
//	            maplibre.WithCenter(maplibre.At(-122.42, 37.77)),
//	            maplibre.WithZoom(11),
//	            maplibre.WithNavigationControl(),
//	        )
//	    }
//	    return nil
//	}
//
//	func (p *Page) View(ctx *via.CtxR) h.H { return p.Map.Mount() }
//
//	func (p *Page) GoToTokyo(ctx *via.Ctx) { p.Map.FlyTo(ctx, maplibre.At(139.69, 35.69), 10) }
//
// # Coordinate order
//
// Coordinates are [lng, lat] (longitude first), the inverse of the lat/lng most
// map UIs print. The camera, marker, and center APIs take a [LngLat] whose
// named fields defuse the swap — build it with maplibre.LngLat{Lng: …, Lat: …}
// (order-independent) or the maplibre.At(lng, lat) shorthand; box APIs take a
// [Bounds] with named edges. GeoJSON geometry ([Point], [LineString],
// [Polygon]) stays as raw [lng, lat] arrays.
//
// # Camera
//
// FlyTo (curved flight), EaseTo (eased hop), JumpTo (instant), plus SetCenter,
// SetZoom, SetPitch, SetBearing, and FitBounds([Bounds]) to frame a box.
//
// # Markers
//
// AddMarker(ctx, id, at, opts…) places a keyed marker (at is a [LngLat]);
// WithMarker declares a static one at construction; MoveMarker
// repositions it live (vehicle tracking), RemoveMarker / ClearMarkers tear
// down. Options: Color, Draggable, Scale, PopupText (XSS-safe, for user
// content), PopupHTML (trusted markup only — MapLibre does not sanitize it).
//
// # Popups (dialogs)
//
// ShowPopup(ctx, id, at, text, opts…) opens a keyed popup at a point — the
// "dialog on click" pattern: from an [OnFeatureClick] / [OnClick] handler,
// ShowPopup at e.LngLat() with details looked up on the server. Re-using an id
// replaces rather than stacks; ClosePopup closes it; ShowPopupHTML takes
// trusted HTML. Options: WithoutCloseButton, WithoutCloseOnClick,
// PopupMaxWidth, PopupClass.
//
//	func (p *Page) Picked(ctx *via.Ctx) {
//	    e := p.Map.Event(ctx)
//	    p.Map.ShowPopup(ctx, "info", e.LngLat(), p.lookupName(e.FeatureID))
//	}
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
// # Styling expressions
//
// Paint/Layout values can be data-driven MapLibre expressions. Instead of
// hand-writing the nested []any arrays, compose them with the typed builders:
// Get (a feature property), FeatureState (runtime state), Zoom, Boolean, Case
// (+ Branch), Interpolate / Step (+ Stop) for zoom/data ramps, and the sugar
// WhenHovered / WhenState for the common highlight case. They return Expr (an
// alias for []any), so they nest freely and stay interchangeable with raw
// []any:
//
//	maplibre.Paint("fill-color", maplibre.WhenHovered("#ffcc00", "#5856d6"))
//	maplibre.Paint("line-width", maplibre.Interpolate(maplibre.Zoom(),
//	    maplibre.Stop{At: 5, Value: 2}, maplibre.Stop{At: 12, Value: 6}))
//
// # Inbound events
//
// Register Go handlers for user gestures so the map drives the server, not
// just the reverse:
//
//   - OnClick — a basemap click (sets Lng/Lat).
//   - OnMoveEnd — the camera settles after a pan/zoom/rotate (sets the center,
//     Zoom, Bearing, Pitch, and the bounding box West/South/East/North).
//   - OnMarkerClick / OnMarkerDragEnd — a marker is clicked or a draggable
//     marker dropped (sets MarkerID and Lng/Lat).
//   - OnFeatureClick(layer, method) — a rendered data-layer feature is clicked
//     (sets FeatureID from the feature's id property, plus the click Lng/Lat).
//
// The handler reads the gesture with [Map.Event]:
//
//	p.Map = maplibre.NewMap(
//	    maplibre.WithElementID("map"),
//	    maplibre.OnClick(p.PlacePin),   // bound method, like the on package
//	    maplibre.OnMoveEnd(p.LoadView),
//	)
//
//	func (p *Page) PlacePin(ctx *via.Ctx) {
//	    e := p.Map.Event(ctx) // e.Lng, e.Lat
//	    p.Map.AddMarker(ctx, "pin", e.LngLat())
//	}
//
//	func (p *Page) LoadView(ctx *via.Ctx) {
//	    e := p.Map.Event(ctx) // e.Zoom plus bounds e.West/South/East/North
//	    // fetch features inside the box, then SetGeoJSON them
//	}
//
// Zoom/Bearing/Pitch carry the live camera on every gesture; the bounding box
// is moveend-only and MarkerID/FeatureID are set only by their own gesture —
// see [MapEvent]. Clickable features and markers show a pointer cursor on
// hover, so they read as interactive. OnMapEvent(name, method) is the escape
// hatch for any other map event (dblclick, contextmenu, zoomend, …).
//
// WithFeatureHover(layer) highlights the hovered feature client-side via
// MapLibre feature-state — no round-trip. Reference ['feature-state','hover']
// in a paint property to style it, and give the source feature ids with
// GenerateFeatureIDs() so the highlight can target them.
//
// # Lifecycle and escape hatch
//
//   - SetStyle swaps the basemap; Resize recomputes layout; Dispose frees the
//     WebGL context and registry slot (call on SPA unmount).
//   - Call(ctx, method, args…) invokes any Map method the typed API misses.
//   - WithMapOption(key, value) sets any constructor option not covered.
//
// # Asset delivery, self-hosting, and CSP
//
// The MapLibre JS + CSS ship embedded in the binary (vendored from the pinned
// v5 release), served at content-hashed /via/assets/maplibre/ paths with
// immutable cache headers — registration does no network I/O and pages
// reference no third-party origin by default.
//
//	maplibre.Plugin(maplibre.WithSource("/static/maplibre-gl.js"),
//	    maplibre.WithStylesheet("/static/maplibre-gl.css")) // self-host
//	maplibre.Plugin(maplibre.WithCSPBuild()) // same-origin worker for strict worker-src
//	maplibre.Plugin(                          // CDN opt-in, SRI mandatory
//	    maplibre.WithCDN("https://cdn.example.com/maplibre-gl.js", "sha384-…"),
//	    maplibre.WithCDNStylesheet("https://cdn.example.com/maplibre-gl.css", "sha384-…"))
//
// The WithCDN options require a well-formed integrity hash for the exact build
// at that URL; the emitted tags carry integrity + crossorigin="anonymous".
// Running a different MapLibre version means supplying its URLs and hashes via
// the CDN options (pin a v5 release — v6 is ESM-only and drops the maplibregl
// global the script include relies on); a bare WithVersion bump panics. The
// CSS is required: without it markers, popups, and controls render unstyled.
// The default style is MapLibre's no-key demo style, intended for demos and
// CI, not production; supply your own via WithStyle.
package maplibre
