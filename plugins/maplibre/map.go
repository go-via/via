package maplibre

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

const defaultStyleURL = "https://demotiles.maplibre.org/style.json"

// mapCounter gives each Map a process-unique registry slot.
var mapCounter atomic.Uint64

// ControlPosition is one of MapLibre's four control anchors. The zero value
// means "MapLibre's default" (top-right).
type ControlPosition string

const (
	TopLeft     ControlPosition = "top-left"
	TopRight    ControlPosition = "top-right"
	BottomLeft  ControlPosition = "bottom-left"
	BottomRight ControlPosition = "bottom-right"
)

type control struct {
	expr string
	pos  ControlPosition
}

type geoSource struct {
	id    string
	data  map[string]any
	genID bool // emit MapLibre generateId:true so features get addressable ids
}

// SourceOption configures a GeoJSON source declared with [WithGeoJSONSource].
type SourceOption func(*geoSource)

// GenerateFeatureIDs sets MapLibre's generateId on the source, so its features
// get stable auto-assigned ids that feature-state ([WithFeatureHover]) and
// selection can target. Use it when your GeoJSON features have no id of their
// own; omit it when you supply your own ids (generateId would override them).
func GenerateFeatureIDs() SourceOption {
	return func(s *geoSource) { s.genID = true }
}

// Map is a MapLibre GL map. Construct with [NewMap], render it in View with
// [Map.Mount], then drive it over SSE from actions or a [via.Stream] ticker
// via the camera ([Map.FlyTo] …), marker ([Map.AddMarker] …), and data
// ([Map.SetGeoJSON] …) methods, or the [Map.Call] escape hatch.
type Map struct {
	seq       uint64
	elementID string
	width     string
	height    string
	classes   []string

	style       string
	lng, lat    float64
	zoom        float64
	pitch       *float64
	bearing     *float64
	minZoom     *float64
	maxZoom     *float64
	maxBounds   *[4]float64
	interactive *bool
	attribution *bool
	hash        bool
	extra       map[string]any

	controls       []control
	sources        []geoSource
	layers         []map[string]any
	handlers       []handler
	markerClick    bool         // attach a click listener to each marker
	markerDragEnd  bool         // attach a dragend listener to each marker
	featureClicks  int          // count of OnFeatureClick handlers, for unique event names
	mapEvents      int          // count of OnMapEvent handlers, for unique event names
	initialMarkers []markerSpec // markers declared at construction via WithMarker
}

// MapOption configures a Map. Options are applied in argument order.
type MapOption func(*Map)

// WithElementID sets the container element id. Panics on ASCII whitespace —
// such an id is valid HTML5 but unaddressable by a `#id` selector, so styling
// and the registry lookup would silently break.
func WithElementID(id string) MapOption {
	if strings.ContainsAny(id, " \t\n\r\f") {
		panic(fmt.Errorf("maplibre: WithElementID: id %q must not contain whitespace", id))
	}
	return func(m *Map) { m.elementID = id }
}

// WithDimensions sets container width and height. An empty side falls back to
// its default ("100%" width, "400px" height) — a map needs an explicit height
// or it collapses to zero and renders nothing.
func WithDimensions(width, height string) MapOption {
	return func(m *Map) {
		m.width = width
		m.height = height
	}
}

// WithClass adds CSS classes to the container. Panics if any single arg
// contains whitespace — "a b" as one arg silently becomes two classes; pass
// them as separate args.
func WithClass(parts ...string) MapOption {
	for _, p := range parts {
		if strings.ContainsAny(p, " \t\n\r\f") {
			panic(fmt.Errorf("maplibre: WithClass: class name %q must not contain whitespace (use separate args)", p))
		}
	}
	return func(m *Map) { m.classes = parts }
}

// WithStyle sets the map style URL (a Style Spec JSON document). The default
// is MapLibre's no-key demo style, which is meant for demos and CI, not a
// production SLA — supply your own style (e.g. a MapTiler/Stadia URL) for
// real use.
func WithStyle(url string) MapOption {
	return func(m *Map) { m.style = url }
}

// WithCenter sets the initial center. [LngLat]'s named fields keep longitude
// and latitude from being swapped.
func WithCenter(at LngLat) MapOption {
	return func(m *Map) {
		m.lng = at.Lng
		m.lat = at.Lat
	}
}

// WithZoom sets the initial zoom level (0 = whole world).
func WithZoom(z float64) MapOption { return func(m *Map) { m.zoom = z } }

// WithPitch tilts the camera from straight-down (0) toward the horizon, in
// degrees (0–85).
func WithPitch(deg float64) MapOption { return func(m *Map) { m.pitch = &deg } }

// WithBearing rotates the map clockwise from north, in degrees.
func WithBearing(deg float64) MapOption { return func(m *Map) { m.bearing = &deg } }

// WithZoomRange clamps the user's zoom between min and max.
func WithZoomRange(min, max float64) MapOption {
	return func(m *Map) {
		m.minZoom = &min
		m.maxZoom = &max
	}
}

// WithMaxBounds restricts panning to the given box. [Bounds]'s named edges
// keep west/south/east/north from being swapped.
func WithMaxBounds(b Bounds) MapOption {
	return func(m *Map) { m.maxBounds = &[4]float64{b.West, b.South, b.East, b.North} }
}

// WithoutInteraction disables all user interaction (pan/zoom/rotate) for a
// static display map driven entirely from the server.
func WithoutInteraction() MapOption {
	return func(m *Map) { f := false; m.interactive = &f }
}

// WithoutAttribution removes the on-map attribution control. Most tile/style
// sources require visible attribution by licence — keep it unless you surface
// the credit elsewhere.
func WithoutAttribution() MapOption {
	return func(m *Map) { f := false; m.attribution = &f }
}

// WithHash syncs the map's center/zoom/bearing/pitch to the URL hash, so a
// shared link reopens the same view.
func WithHash() MapOption { return func(m *Map) { m.hash = true } }

// WithNavigationControl adds zoom and compass buttons, optionally at a given
// position (default top-right).
func WithNavigationControl(pos ...ControlPosition) MapOption {
	return addControl("new maplibregl.NavigationControl({})", pos)
}

// WithScaleControl adds a distance scale bar.
func WithScaleControl(pos ...ControlPosition) MapOption {
	return addControl("new maplibregl.ScaleControl({})", pos)
}

// WithGeolocateControl adds a "find my location" button wired for
// high-accuracy tracking.
func WithGeolocateControl(pos ...ControlPosition) MapOption {
	return addControl("new maplibregl.GeolocateControl({positionOptions:{enableHighAccuracy:true},trackUserLocation:true})", pos)
}

// WithFullscreenControl adds a fullscreen toggle.
func WithFullscreenControl(pos ...ControlPosition) MapOption {
	return addControl("new maplibregl.FullscreenControl({})", pos)
}

func addControl(expr string, pos []ControlPosition) MapOption {
	var p ControlPosition
	if len(pos) > 0 {
		p = pos[0]
	}
	return func(m *Map) { m.controls = append(m.controls, control{expr: expr, pos: p}) }
}

// WithGeoJSONSource registers a GeoJSON source added once the style loads.
// Pair with [WithLayer] to draw it. data is a GeoJSON object (see
// [FeatureCollection]); the runtime [Map.SetGeoJSON] updates it later.
func WithGeoJSONSource(id string, data map[string]any, opts ...SourceOption) MapOption {
	return func(m *Map) {
		s := geoSource{id: id, data: data}
		for _, o := range opts {
			o(&s)
		}
		m.sources = append(m.sources, s)
	}
}

// WithLayer registers a layer added once the style loads. Build spec with
// [CircleLayer] / [LineLayer] / [FillLayer] / [SymbolLayer], or pass a raw
// layer spec.
func WithLayer(spec map[string]any) MapOption {
	return func(m *Map) { m.layers = append(m.layers, spec) }
}

// WithMapOption is the escape hatch for any MapLibre Map constructor option
// the typed helpers don't cover (e.g. "projection", "renderWorldCopies").
// Panics if value can't be marshalled to JSON — a programmer bug surfaced
// eagerly rather than at render time.
func WithMapOption(key string, value any) MapOption {
	if _, err := json.Marshal(value); err != nil {
		panic(fmt.Errorf("maplibre: WithMapOption %q: %v", key, err))
	}
	return func(m *Map) {
		if m.extra == nil {
			m.extra = map[string]any{}
		}
		m.extra[key] = value
	}
}

// NewMap creates a Map. Without [WithElementID] the id is auto-generated from
// a monotonic counter; without [WithStyle] the no-key demo style is used.
func NewMap(opts ...MapOption) *Map {
	m := &Map{seq: mapCounter.Add(1)}
	for _, opt := range opts {
		opt(m)
	}
	if m.elementID == "" {
		m.elementID = fmt.Sprintf("maplibre-%d", m.seq)
	}
	if m.style == "" {
		m.style = defaultStyleURL
	}
	return m
}

// Mount returns the container element with the inline init script. Render it
// in View. The container carries data-ignore-morph so datastar's DOM morphing
// can't tear out MapLibre's WebGL canvas on a re-render.
func (m *Map) Mount() h.H {
	width := cmp.Or(m.width, "100%")
	height := cmp.Or(m.height, "400px")
	kids := []h.H{
		h.ID(m.elementID),
		h.Class(m.classes...),
		h.DataIgnoreMorph(),
		h.Style(fmt.Sprintf("width:%s;height:%s", width, height)),
	}
	for _, hd := range m.handlers {
		// A handler with no domEvent is client-only JS (e.g. WithFeatureHover):
		// it contributes its js in initJS but must not emit a data-on attribute
		// — an empty data-on: is malformed and datastar rejects it.
		if hd.domEvent != "" {
			kids = append(kids, h.Data("on:"+hd.domEvent, hd.expr))
		}
	}
	kids = append(kids, h.Script(h.Raw(m.initJS())))
	return h.Div(kids...)
}

func (m *Map) constructorOptions() map[string]any {
	opts := map[string]any{
		"container": m.elementID,
		"style":     m.style,
		"center":    []float64{m.lng, m.lat},
		"zoom":      m.zoom,
	}
	if m.pitch != nil {
		opts["pitch"] = *m.pitch
	}
	if m.bearing != nil {
		opts["bearing"] = *m.bearing
	}
	if m.minZoom != nil {
		opts["minZoom"] = *m.minZoom
	}
	if m.maxZoom != nil {
		opts["maxZoom"] = *m.maxZoom
	}
	if m.maxBounds != nil {
		b := *m.maxBounds
		opts["maxBounds"] = [][]float64{{b[0], b[1]}, {b[2], b[3]}}
	}
	if m.interactive != nil {
		opts["interactive"] = *m.interactive
	}
	if m.attribution != nil {
		opts["attributionControl"] = *m.attribution
	}
	if m.hash {
		opts["hash"] = true
	}
	for k, v := range m.extra {
		opts[k] = v
	}
	return opts
}

func (m *Map) initJS() string {
	var b strings.Builder
	fmt.Fprintf(&b, "(function(){window.__viaMaps=window.__viaMaps||{};")
	fmt.Fprintf(&b, "var _el=document.getElementById(%s);", mustJSON(m.elementID))
	fmt.Fprintf(&b, "var _m=new maplibregl.Map(%s);", mustJSON(m.constructorOptions()))

	for _, c := range m.controls {
		if c.pos != "" {
			fmt.Fprintf(&b, "_m.addControl(%s,%s);", c.expr, mustJSON(string(c.pos)))
		} else {
			fmt.Fprintf(&b, "_m.addControl(%s);", c.expr)
		}
	}

	// Sources and layers must be added after the style loads, or MapLibre
	// throws "Style is not done loading".
	if len(m.sources) > 0 || len(m.layers) > 0 {
		b.WriteString("_m.on('load',function(){")
		for _, s := range m.sources {
			src := geoJSONSource(s.data)
			if s.genID {
				src["generateId"] = true
			}
			fmt.Fprintf(&b, "_m.addSource(%s,%s);", mustJSON(s.id), mustJSON(src))
		}
		for _, l := range m.layers {
			fmt.Fprintf(&b, "_m.addLayer(%s);", mustJSON(l))
		}
		b.WriteString("});")
	}

	for _, hd := range m.handlers {
		b.WriteString(hd.js)
	}

	fmt.Fprintf(&b, "var _ro=new ResizeObserver(function(){var _e=window.__viaMaps[%d];if(_e&&_e.m)_e.m.resize()});", m.seq)
	b.WriteString("_ro.observe(_el);")
	fmt.Fprintf(&b, "window.__viaMaps[%d]={m:_m,ro:_ro,markers:{}};", m.seq)

	// Construction markers run last — they look up the registry entry just
	// assigned above as _e, so they must come after it.
	for _, ms := range m.initialMarkers {
		b.WriteString(m.markerScript(ms.id, ms.at, ms.cfg))
	}

	b.WriteString("})();")
	return b.String()
}

// mapRef is the optional-chaining expression resolving to this map's instance
// (or undefined). Prefix it for method calls: mapRef() + "?.flyTo(...)".
func (m *Map) mapRef() string {
	return fmt.Sprintf("window.__viaMaps?.[%d]?.m", m.seq)
}

// execReady runs body (with `_m` bound to the map instance) once the style is
// loaded — the robust guard for source/layer ops that throw before load.
func (m *Map) execReady(ctx *via.Ctx, body string) {
	ctx.ExecScript(fmt.Sprintf(
		"(function(){var _e=window.__viaMaps&&window.__viaMaps[%d];if(!_e||!_e.m)return;window.__viaMapReady(_e.m,function(){var _m=_e.m;%s})})()",
		m.seq, body,
	))
}

func geoJSONSource(data map[string]any) map[string]any {
	return map[string]any{"type": "geojson", "data": data}
}

// mustJSON marshals v with HTML escaping on (Go's default), so `<`, `>`, `&`
// become \uXXXX — a string value can't break out of the surrounding <script>
// with a literal `</script>`. Callers pass pre-validated or scalar values, so
// a marshal error here is an internal bug.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("maplibre: internal mustJSON: %v", err))
	}
	return string(b)
}

// renderH renders an [h.H] node to its HTML string for use as popup content. A
// nil node renders as empty. Rendering into a strings.Builder never errors, so
// the render error is dropped. Content built with h.T is escaped, so it's safe
// even for user data; only h.Raw is unescaped.
func renderH(content h.H) string {
	if content == nil {
		return ""
	}
	var sb strings.Builder
	_ = content.Render(&sb)
	return sb.String()
}
