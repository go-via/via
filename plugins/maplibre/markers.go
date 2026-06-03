package maplibre

import (
	"fmt"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type markerConfig struct {
	opts      map[string]any
	popupText *string
	popupHTML *string
}

// MarkerOption configures a marker created by [Map.AddMarker].
type MarkerOption func(*markerConfig)

// Color tints the default teardrop pin (a CSS color). Ignored if you supply a
// custom element — color only affects the built-in SVG pin.
func Color(c string) MarkerOption {
	return func(m *markerConfig) { m.opts["color"] = c }
}

// Draggable lets the user drag the marker. Pair with [OnMarkerDragEnd] if you
// need the moved position back in a Go handler.
func Draggable() MarkerOption {
	return func(m *markerConfig) { m.opts["draggable"] = true }
}

// Scale multiplies the default pin size (1 = default).
func Scale(s float64) MarkerOption {
	return func(m *markerConfig) { m.opts["scale"] = s }
}

// PopupText attaches a click-to-open popup showing text as a plain DOM text
// node — XSS-safe, so it's the right choice for user-supplied content. The
// text is JSON-encoded into the emitted script. PopupText and [PopupHTML] are
// mutually exclusive; the last one set wins.
func PopupText(text string) MarkerOption {
	return func(m *markerConfig) { m.popupText = &text; m.popupHTML = nil }
}

// PopupHTML attaches a popup whose body is an [h.H] node, composed with the
// same h.* builders as your view. Content built with h.T is escaped (safe for
// user data); h.Raw is injected unescaped and MapLibre does not sanitize it, so
// only use h.Raw with trusted markup. PopupHTML and [PopupText] are mutually
// exclusive; the last one set wins.
func PopupHTML(content h.H) MarkerOption {
	return func(m *markerConfig) { s := renderH(content); m.popupHTML = &s; m.popupText = nil }
}

// AddMarker places (or replaces) a marker keyed by id at a [LngLat]. The id
// lets later [Map.MoveMarker] / [Map.RemoveMarker] calls address this exact
// marker — pass a stable key per logical pin (a vehicle id, a search-result
// id). Re-adding the same id removes the previous marker first, so a stream
// can re-emit a marker without stacking duplicates.
func (m *Map) AddMarker(ctx *via.Ctx, id string, at LngLat, opts ...MarkerOption) {
	cfg := &markerConfig{opts: map[string]any{}}
	for _, o := range opts {
		o(cfg)
	}
	ctx.ExecScript(m.markerScript(id, at, cfg))
}

// markerScript builds the self-invoking script that creates (or replaces) a
// keyed marker on this map. Shared by [Map.AddMarker] (runtime, over SSE) and
// [WithMarker] (construction, inlined in initJS after the registry entry
// exists). It reads m.markerClick / m.markerDragEnd, so the click/drag wiring
// is decided at render time, after all options are applied.
func (m *Map) markerScript(id string, at LngLat, cfg *markerConfig) string {
	var b strings.Builder
	jid := mustJSON(id)
	fmt.Fprintf(&b, "var _e=window.__viaMaps&&window.__viaMaps[%d];if(!_e||!_e.m)return;", m.seq)
	fmt.Fprintf(&b, "if(_e.markers[%s])_e.markers[%s].remove();", jid, jid)
	fmt.Fprintf(&b, "var _mk=new maplibregl.Marker(%s).setLngLat(%s);",
		mustJSON(cfg.opts), mustJSON(at.pair()))
	if cfg.popupText != nil {
		fmt.Fprintf(&b, "_mk.setPopup(new maplibregl.Popup({offset:25}).setText(%s));", mustJSON(*cfg.popupText))
	} else if cfg.popupHTML != nil {
		fmt.Fprintf(&b, "_mk.setPopup(new maplibregl.Popup({offset:25}).setHTML(%s));", mustJSON(*cfg.popupHTML))
	}
	if m.markerClick {
		b.WriteString("_mk.getElement().style.cursor='pointer';")
		fmt.Fprintf(&b, "_mk.getElement().addEventListener('click',function(ev){ev.stopPropagation();var ll=_mk.getLngLat();_e.m.getContainer().dispatchEvent(new CustomEvent('viamarkerclick',{detail:{id:%s,lng:ll.lng,lat:ll.lat,%s}}))});", jid, cameraDetail("_e.m"))
	}
	if m.markerDragEnd {
		fmt.Fprintf(&b, "_mk.on('dragend',function(){var ll=_mk.getLngLat();_e.m.getContainer().dispatchEvent(new CustomEvent('viamarkerdragend',{detail:{id:%s,lng:ll.lng,lat:ll.lat,%s}}))});", jid, cameraDetail("_e.m"))
	}
	fmt.Fprintf(&b, "_mk.addTo(_e.m);_e.markers[%s]=_mk;", jid)
	// Terminate the IIFE with a semicolon: in initJS, construction markers are
	// concatenated back-to-back, and `})()` followed by the next `(function(){`
	// would parse as CALLING the first IIFE's (undefined) return value with the
	// second as an argument — a runtime TypeError aborting every later marker.
	return "(function(){" + b.String() + "})();"
}

// markerSpec is a marker declared at construction with [WithMarker].
type markerSpec struct {
	id  string
	at  LngLat
	cfg *markerConfig
}

// WithMarker places a static marker at construction, so a map with fixed pins
// needs no OnConnect/AddMarker — it renders in the initial HTML. The id keys it
// for later [Map.MoveMarker] / [Map.RemoveMarker]; it honors the same
// [MarkerOption]s and the same [OnMarkerClick] / [OnMarkerDragEnd] wiring as a
// runtime marker. Drive markers that appear or move after load from an action
// or ticker with [Map.AddMarker] instead.
func WithMarker(id string, at LngLat, opts ...MarkerOption) MapOption {
	cfg := &markerConfig{opts: map[string]any{}}
	for _, o := range opts {
		o(cfg)
	}
	return func(m *Map) {
		m.initialMarkers = append(m.initialMarkers, markerSpec{id: id, at: at, cfg: cfg})
	}
}

// MoveMarker repositions an existing marker to at without recreating it — the
// smooth path for live tracking (a moving vehicle, a cursor). A no-op if no
// marker holds that id.
func (m *Map) MoveMarker(ctx *via.Ctx, id string, at LngLat) {
	jid := mustJSON(id)
	ctx.ExecScript(fmt.Sprintf(
		"(function(){var _e=window.__viaMaps&&window.__viaMaps[%d];var _mk=_e&&_e.markers&&_e.markers[%s];if(_mk)_mk.setLngLat(%s)})()",
		m.seq, jid, mustJSON(at.pair())))
}

// RemoveMarker removes the marker with the given id. A no-op if absent.
func (m *Map) RemoveMarker(ctx *via.Ctx, id string) {
	jid := mustJSON(id)
	ctx.ExecScript(fmt.Sprintf(
		"(function(){var _e=window.__viaMaps&&window.__viaMaps[%d];var _mk=_e&&_e.markers&&_e.markers[%s];if(_mk){_mk.remove();delete _e.markers[%s]}})()",
		m.seq, jid, jid))
}

// ClearMarkers removes every marker added via [Map.AddMarker].
func (m *Map) ClearMarkers(ctx *via.Ctx) {
	ctx.ExecScript(fmt.Sprintf(
		"(function(){var _e=window.__viaMaps&&window.__viaMaps[%d];if(_e&&_e.markers){Object.keys(_e.markers).forEach(function(k){_e.markers[k].remove()});_e.markers={}}})()",
		m.seq))
}
