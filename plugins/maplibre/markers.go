package maplibre

import (
	"fmt"
	"strings"

	"github.com/go-via/via"
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

// Draggable lets the user drag the marker. Pair with a dragend listener (via
// a raw style/script) if you need the moved position back.
func Draggable() MarkerOption {
	return func(m *markerConfig) { m.opts["draggable"] = true }
}

// Scale multiplies the default pin size (1 = default).
func Scale(s float64) MarkerOption {
	return func(m *markerConfig) { m.opts["scale"] = s }
}

// PopupText attaches a click-to-open popup showing text as a plain DOM text
// node — XSS-safe, so it's the right choice for user-supplied content. The
// text is JSON-encoded into the emitted script.
func PopupText(text string) MarkerOption {
	return func(m *markerConfig) { m.popupText = &text }
}

// PopupHTML attaches a popup whose body is raw HTML. MapLibre does NOT
// sanitize it, so use this ONLY with trusted, developer-controlled markup —
// never with user input (use [PopupText] for that). The string is
// JSON-encoded so it can't break out of the script, but the rendered HTML
// still executes in the popup. PopupHTML and PopupText are mutually
// exclusive; the last one set wins.
func PopupHTML(html string) MarkerOption {
	return func(m *markerConfig) { m.popupHTML = &html }
}

// AddMarker places (or replaces) a marker keyed by id at (lng, lat). The id
// lets later [Map.MoveMarker] / [Map.RemoveMarker] calls address this exact
// marker — pass a stable key per logical pin (a vehicle id, a search-result
// id). Re-adding the same id removes the previous marker first, so a stream
// can re-emit a marker without stacking duplicates. Coordinates are
// [lng, lat].
func (m *Map) AddMarker(ctx *via.Ctx, id string, lng, lat float64, opts ...MarkerOption) {
	cfg := &markerConfig{opts: map[string]any{}}
	for _, o := range opts {
		o(cfg)
	}

	var b strings.Builder
	jid := mustJSON(id)
	fmt.Fprintf(&b, "var _e=window.__viaMaps&&window.__viaMaps[%d];if(!_e||!_e.m)return;", m.seq)
	fmt.Fprintf(&b, "if(_e.markers[%s])_e.markers[%s].remove();", jid, jid)
	fmt.Fprintf(&b, "var _mk=new maplibregl.Marker(%s).setLngLat(%s);",
		mustJSON(cfg.opts), mustJSON([]float64{lng, lat}))
	if cfg.popupText != nil {
		fmt.Fprintf(&b, "_mk.setPopup(new maplibregl.Popup({offset:25}).setText(%s));", mustJSON(*cfg.popupText))
	} else if cfg.popupHTML != nil {
		fmt.Fprintf(&b, "_mk.setPopup(new maplibregl.Popup({offset:25}).setHTML(%s));", mustJSON(*cfg.popupHTML))
	}
	fmt.Fprintf(&b, "_mk.addTo(_e.m);_e.markers[%s]=_mk;", jid)
	ctx.ExecScript("(function(){" + b.String() + "})()")
}

// MoveMarker repositions an existing marker to (lng, lat) without recreating
// it — the smooth path for live tracking (a moving vehicle, a cursor). A no-op
// if no marker holds that id.
func (m *Map) MoveMarker(ctx *via.Ctx, id string, lng, lat float64) {
	jid := mustJSON(id)
	ctx.ExecScript(fmt.Sprintf(
		"(function(){var _e=window.__viaMaps&&window.__viaMaps[%d];var _mk=_e&&_e.markers&&_e.markers[%s];if(_mk)_mk.setLngLat(%s)})()",
		m.seq, jid, mustJSON([]float64{lng, lat})))
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
