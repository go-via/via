package maplibre

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/internal/spec"
)

// jsString encodes s as a JS string literal safe to inline in any quoting
// context. mustJSON (json.Marshal) escapes <, >, &, " and backslash, but NOT
// the single quote; escaping ' as well means the literal can't break out even
// when adjacent to single-quoted JS. Used for developer-supplied identifiers
// (layer ids, feature-property names, MapLibre event names) that go into the
// _m.on('event', <here>, …) call sites.
func jsString(s string) string {
	return strings.ReplaceAll(mustJSON(s), "'", `'`)
}

// MapEvent is the payload a user gesture carries back to a Go handler. The
// browser writes these fields as datastar signals before posting the action;
// [Map.Event] decodes them. Which fields a given gesture sets: a click sets
// Lng/Lat; a moveend sets the center (Lng/Lat), Zoom, Bearing, Pitch, and the
// visible bounding box (West/South/East/North).
//
// Zoom, Bearing, and Pitch carry the LIVE camera on every gesture, so they are
// always valid. Lng/Lat are gesture-specific (click point, marker position,
// feature-click point, or moveend center). The bounding box (West/South/East/
// North) is set only by a moveend; MarkerID only by a marker gesture; FeatureID
// only by a feature click. Those last fields are shared signals that carry over
// their previous value across gesture kinds, so read them only in the handler
// of the gesture that sets them.
//
// Coordinates are [lng, lat] degrees, matching the rest of the package.
type MapEvent struct {
	MarkerID  string  `form:"viaMapId"`
	FeatureID string  `form:"viaMapFeatureId"`
	Lng       float64 `form:"viaMapLng"`
	Lat       float64 `form:"viaMapLat"`
	Zoom      float64 `form:"viaMapZoom"`
	Bearing   float64 `form:"viaMapBearing"`
	Pitch     float64 `form:"viaMapPitch"`
	West      float64 `form:"viaMapW"`
	South     float64 `form:"viaMapS"`
	East      float64 `form:"viaMapE"`
	North     float64 `form:"viaMapN"`
}

// LngLat is the gesture's position as a [LngLat], so it flows straight into the
// camera and marker APIs without re-typing the fields:
//
//	e := p.Map.Event(ctx)
//	p.Map.AddMarker(ctx, "pin", e.LngLat())
func (e MapEvent) LngLat() LngLat { return LngLat{Lng: e.Lng, Lat: e.Lat} }

// cameraExpr assigns the live camera signals from a gesture's evt.detail. Every
// gesture prepends it (before @post) so [MapEvent] Zoom/Bearing/Pitch are
// always fresh, not a leftover from the last moveend.
const cameraExpr = "$viaMapZoom=evt.detail.zoom;$viaMapBearing=evt.detail.bearing;$viaMapPitch=evt.detail.pitch;"

// cameraDetail is the JS object-literal fragment reading the live camera off
// the map handle (mapVar is "_m" for map-level listeners, "_e.m" for markers).
func cameraDetail(mapVar string) string {
	return fmt.Sprintf("zoom:%s.getZoom(),bearing:%s.getBearing(),pitch:%s.getPitch()", mapVar, mapVar, mapVar)
}

// handler pairs a browser-side custom event with the datastar expression that
// captures its detail into signals and posts the bound action.
type handler struct {
	domEvent string // custom event the container listens for (e.g. "viamapclick")
	expr     string // datastar expression: assign signals from evt.detail, then @post
	js       string // initJS body that subscribes to the MapLibre event and dispatches domEvent
}

// OnClick registers a bound method as the handler for a basemap click. The
// clicked [lng, lat] arrives in the [MapEvent] read by [Map.Event]. Panics if
// fn is not a bound method value (e.g. a closure), matching the on package —
// a closure has no stable action name to POST to.
func OnClick[F via.Action](fn F) MapOption {
	method := methodName("OnClick", fn)
	return func(m *Map) {
		m.handlers = append(m.handlers, handler{
			domEvent: "viamapclick",
			expr:     "$viaMapLng=evt.detail.lng;$viaMapLat=evt.detail.lat;" + cameraExpr + "@post('/_action/" + method + "')",
			js:       "_m.on('click',function(e){_el.dispatchEvent(new CustomEvent('viamapclick',{detail:{lng:e.lngLat.lng,lat:e.lngLat.lat," + cameraDetail("_m") + "}}))});",
		})
	}
}

// OnMoveEnd registers a bound method to run after the camera settles from a
// pan, zoom, or rotate. The [MapEvent] carries the new center (Lng/Lat), Zoom,
// Bearing, Pitch, and the visible bounding box — everything a server needs to
// load what's now in view. Panics like [OnClick] if fn is not a bound method.
func OnMoveEnd[F via.Action](fn F) MapOption {
	method := methodName("OnMoveEnd", fn)
	return func(m *Map) {
		m.handlers = append(m.handlers, handler{
			domEvent: "viamapmove",
			expr: "$viaMapLng=evt.detail.lng;$viaMapLat=evt.detail.lat;$viaMapZoom=evt.detail.zoom;" +
				"$viaMapBearing=evt.detail.bearing;$viaMapPitch=evt.detail.pitch;" +
				"$viaMapW=evt.detail.w;$viaMapS=evt.detail.s;$viaMapE=evt.detail.e;$viaMapN=evt.detail.n;" +
				"@post('/_action/" + method + "')",
			js: "_m.on('moveend',function(){var c=_m.getCenter(),b=_m.getBounds();" +
				"_el.dispatchEvent(new CustomEvent('viamapmove',{detail:{lng:c.lng,lat:c.lat," +
				cameraDetail("_m") + "," +
				"w:b.getWest(),s:b.getSouth(),e:b.getEast(),n:b.getNorth()}}))});",
		})
	}
}

// OnMarkerClick registers a bound method to run when any marker is clicked.
// The [MapEvent] carries the clicked marker's id ([MapEvent.MarkerID]) and its
// position (Lng/Lat) — enough to select a feature. The marker's click stops
// propagating, so a basemap [OnClick] does not also fire. Panics like
// [OnClick] if fn is not a bound method.
func OnMarkerClick[F via.Action](fn F) MapOption {
	method := methodName("OnMarkerClick", fn)
	return func(m *Map) {
		m.markerClick = true
		m.handlers = append(m.handlers, handler{
			domEvent: "viamarkerclick",
			expr:     "$viaMapId=evt.detail.id;$viaMapLng=evt.detail.lng;$viaMapLat=evt.detail.lat;" + cameraExpr + "@post('/_action/" + method + "')",
		})
	}
}

// OnMarkerDragEnd registers a bound method to run when the user drops a
// draggable marker (pair with [Draggable]). The [MapEvent] carries the
// marker's id and its new Lng/Lat. Panics like [OnClick] if fn is not a bound
// method.
func OnMarkerDragEnd[F via.Action](fn F) MapOption {
	method := methodName("OnMarkerDragEnd", fn)
	return func(m *Map) {
		m.markerDragEnd = true
		m.handlers = append(m.handlers, handler{
			domEvent: "viamarkerdragend",
			expr:     "$viaMapId=evt.detail.id;$viaMapLng=evt.detail.lng;$viaMapLat=evt.detail.lat;" + cameraExpr + "@post('/_action/" + method + "')",
		})
	}
}

// OnFeatureClick registers a bound method to run when the user clicks a
// rendered feature in the named layer (declared with [WithLayer] / [Map.AddLayer]).
// The [MapEvent] carries the clicked feature's identifier in
// [MapEvent.FeatureID] and the click position (Lng/Lat). idProperty names the
// feature property holding the identifier (default "id"); if that property is
// absent the GeoJSON feature id is used. Register the same layer twice (or two
// layers) for independent handlers. Panics like [OnClick] if fn is not a bound
// method.
//
// This is the server-authoritative selection pattern: the click reports which
// feature was picked, and the handler looks up that feature's data on the
// server rather than trusting client-sent properties.
func OnFeatureClick[F via.Action](layerID string, fn F, idProperty ...string) MapOption {
	method := methodName("OnFeatureClick", fn)
	idProp := "id"
	if len(idProperty) > 0 {
		idProp = idProperty[0]
	}
	return func(m *Map) {
		dom := "viafeatureclick" + strconv.Itoa(m.featureClicks)
		m.featureClicks++
		m.handlers = append(m.handlers, handler{
			domEvent: dom,
			expr:     "$viaMapFeatureId=evt.detail.fid;$viaMapLng=evt.detail.lng;$viaMapLat=evt.detail.lat;" + cameraExpr + "@post('/_action/" + method + "')",
			js: fmt.Sprintf("_m.on('click',%s,function(e){var f=e.features&&e.features[0];if(!f)return;"+
				"var p=f.properties||{};var fid=(p[%s]!=null?p[%s]:f.id);"+
				"_el.dispatchEvent(new CustomEvent('%s',{detail:{fid:fid,lng:e.lngLat.lng,lat:e.lngLat.lat,"+cameraDetail("_m")+"}}))});"+
				"_m.on('mouseenter',%s,function(){_m.getCanvas().style.cursor='pointer'});"+
				"_m.on('mouseleave',%s,function(){_m.getCanvas().style.cursor=''});",
				jsString(layerID), jsString(idProp), jsString(idProp), dom,
				jsString(layerID), jsString(layerID)),
		})
	}
}

// OnMapEvent is the escape hatch for any MapLibre map-level event the typed
// handlers don't cover — double-click, right-click, zoom/rotate end, drag, and
// the rest. It wires _m.on(eventName, …) and routes the gesture to the bound
// method, read via [Map.Event]. The live camera (Zoom/Bearing/Pitch) always
// arrives; Lng/Lat carry the event's pointer position for pointer events
// (dblclick, contextmenu, mousedown, …) and are 0 for events without one
// (zoomend, rotateend, …). Panics like [OnClick] if fn is not a bound method.
//
//	maplibre.OnMapEvent("dblclick", p.ZoomToHere)
//	maplibre.OnMapEvent("contextmenu", p.OpenMenu) // right-click
func OnMapEvent[F via.Action](eventName string, fn F) MapOption {
	method := methodName("OnMapEvent", fn)
	return func(m *Map) {
		dom := "viamapevent" + strconv.Itoa(m.mapEvents)
		m.mapEvents++
		m.handlers = append(m.handlers, handler{
			domEvent: dom,
			expr:     "$viaMapLng=evt.detail.lng;$viaMapLat=evt.detail.lat;" + cameraExpr + "@post('/_action/" + method + "')",
			js: fmt.Sprintf("_m.on(%s,function(e){_el.dispatchEvent(new CustomEvent('%s',"+
				"{detail:{lng:(e&&e.lngLat?e.lngLat.lng:0),lat:(e&&e.lngLat?e.lngLat.lat:0),%s}}))});",
				jsString(eventName), dom, cameraDetail("_m")),
		})
	}
}

// WithFeatureHover highlights the feature under the cursor in the named layer,
// entirely client-side (MapLibre feature-state — no server round-trip, so it
// feels instant). Reference the state in a paint property to style the
// highlight, e.g. a data-driven fill color:
//
//	maplibre.FillLayer("zones", "zones", maplibre.Paint("fill-color",
//	    []any{"case", []any{"boolean", []any{"feature-state", "hover"}, false},
//	        "#ffcc00", "#5856d6"}))
//
// The layer's source must give its features ids that feature-state can target —
// add [GenerateFeatureIDs] to the source, or supply your own feature ids.
func WithFeatureHover(layerID string) MapOption {
	return func(m *Map) {
		m.handlers = append(m.handlers, handler{
			js: fmt.Sprintf("(function(){var _h=null;"+
				"_m.on('mousemove',%s,function(e){if(e.features.length>0){var f=e.features[0];"+
				"if(_h)_m.setFeatureState(_h,{hover:false});_h={source:f.source,id:f.id};_m.setFeatureState(_h,{hover:true})}});"+
				"_m.on('mouseleave',%s,function(){if(_h)_m.setFeatureState(_h,{hover:false});_h=null});})();",
				jsString(layerID), jsString(layerID)),
		})
	}
}

// Event decodes the gesture payload carried by the action POST into a typed
// [MapEvent]. Call it at the top of any handler registered with [OnClick] /
// [OnMoveEnd] / [OnMarkerClick] / [OnMarkerDragEnd] / [OnFeatureClick] /
// [OnMapEvent]. Read only the fields the firing gesture sets — see [MapEvent]
// on why untouched fields are not reliably zero.
func (m *Map) Event(ctx *via.Ctx) MapEvent {
	var e MapEvent
	via.DecodeForm(ctx, &e)
	return e
}

// methodName resolves a bound method to its action name, panicking with the
// on-package-style message when fn is not a bound method.
func methodName(opt string, fn any) string {
	name := spec.MethodName(fn)
	if name == "" {
		panic("maplibre: " + opt + " requires a bound method value (e.g. maplibre.OnClick(p.Place)); got a closure or non-method")
	}
	return name
}
