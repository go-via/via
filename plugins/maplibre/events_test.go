package maplibre_test

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventPage binds a Map with both inbound-event handlers registered and
// surfaces what Map.Event decoded back into patched signals, so a fired
// action can be asserted on through the SSE frame.
type eventPage struct {
	Map *maplibre.Map
}

func (p *eventPage) OnInit(ctx *via.Ctx) error {
	if p.Map == nil {
		p.Map = maplibre.NewMap(
			maplibre.WithElementID("m"),
			maplibre.OnClick(p.PlacePin),
			maplibre.OnMoveEnd(p.Recenter),
			maplibre.OnMarkerClick(p.Selected),
			maplibre.OnMarkerDragEnd(p.Moved),
			maplibre.OnFeatureClick("countries", p.Picked),
		)
	}
	return nil
}

func (p *eventPage) View(ctx *via.CtxR) h.H { return p.Map.Mount() }

// AddPin emits a draggable marker so the SSE frame carries its listener JS.
func (p *eventPage) AddPin(ctx *via.Ctx) {
	p.Map.AddMarker(ctx, "car", maplibre.At(1, 2), maplibre.Draggable())
}

// Selected / Moved echo the marker identity + position the gesture posted.
func (p *eventPage) Selected(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	ctx.Patch.Signals(map[string]any{"gotId": e.MarkerID, "gotLng": e.Lng, "gotLat": e.Lat})
}

func (p *eventPage) Moved(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	ctx.Patch.Signals(map[string]any{"gotId": e.MarkerID, "gotLng": e.Lng, "gotLat": e.Lat})
}

// Picked echoes which feature the user clicked in a data layer.
func (p *eventPage) Picked(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	ctx.Patch.Signals(map[string]any{"gotFid": e.FeatureID, "gotLng": e.Lng, "gotLat": e.Lat})
}

// PlacePin echoes the clicked coordinates back as signals.
func (p *eventPage) PlacePin(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	ctx.Patch.Signals(map[string]any{"gotLng": e.Lng, "gotLat": e.Lat})
}

// Recenter echoes the settled viewport back as signals.
func (p *eventPage) Recenter(ctx *via.Ctx) {
	e := p.Map.Event(ctx)
	ctx.Patch.Signals(map[string]any{
		"gotZoom": e.Zoom, "gotBearing": e.Bearing, "gotPitch": e.Pitch,
		"gotW": e.West, "gotS": e.South, "gotE": e.East, "gotN": e.North,
	})
}

// newEventMap builds a Map with the given handlers, off-page, for render
// assertions. The receiver supplies bound methods spec.MethodName can name.
func newEventMap(opts ...maplibre.MapOption) *maplibre.Map {
	base := []maplibre.MapOption{maplibre.WithElementID("m")}
	return maplibre.NewMap(append(base, opts...)...)
}

func TestMap_OnClick_letsTheBasemapDriveAGoAction(t *testing.T) {
	t.Parallel()
	// A click handler is the foundation of click-to-place: the basemap click
	// must reach a named Go action carrying the clicked lng/lat as signals.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnClick(p.PlacePin)))

	assert.Contains(t, html, "data-on:viamapclick",
		"the container must listen for the dispatched click event")
	assert.Contains(t, html, "/_action/PlacePin",
		"the click must POST to the bound method's action endpoint")
	assert.Contains(t, html, "$viaMapLng=evt.detail.lng",
		"the clicked longitude must be written to the shared signal")
	assert.Contains(t, html, "$viaMapLat=evt.detail.lat",
		"the clicked latitude must be written to the shared signal")
	assert.Contains(t, html, ".on('click'",
		"initJS must subscribe to MapLibre's click event")
	assert.Contains(t, html, "CustomEvent('viamapclick'",
		"the click listener must dispatch the custom event the container listens for")
	assert.Contains(t, html, "e.lngLat.lng",
		"the dispatched detail must carry MapLibre's lngLat")
}

func TestMap_OnMoveEnd_letsCameraSettleDriveAGoAction(t *testing.T) {
	t.Parallel()
	// moveend powers viewport-driven loading: after a pan/zoom the server
	// needs the new center, zoom, and bounding box to fetch what's in view.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnMoveEnd(p.Recenter)))

	assert.Contains(t, html, "data-on:viamapmove",
		"the container must listen for the dispatched move event")
	assert.Contains(t, html, "/_action/Recenter",
		"the move must POST to the bound method's action endpoint")
	// Every viewport field a moveend carries must be written, or a handler
	// reading e.Bearing/e.Pitch/e.South/e.East silently gets stale zeros.
	for _, sig := range []string{
		"$viaMapZoom=evt.detail.zoom", "$viaMapBearing=evt.detail.bearing",
		"$viaMapPitch=evt.detail.pitch", "$viaMapW=evt.detail.w",
		"$viaMapS=evt.detail.s", "$viaMapE=evt.detail.e", "$viaMapN=evt.detail.n",
	} {
		assert.Contains(t, html, sig, "moveend must write every viewport signal")
	}
	assert.Contains(t, html, ".on('moveend'",
		"initJS must subscribe to MapLibre's moveend event")
	for _, read := range []string{
		"getBounds()", "getCenter()", "getZoom()", "getBearing()", "getPitch()",
		"getWest()", "getSouth()", "getEast()", "getNorth()",
	} {
		assert.Contains(t, html, read, "the moveend listener must read every viewport value")
	}
}

func TestMap_OnClick_carriesLiveCameraSoZoomIsNeverStale(t *testing.T) {
	t.Parallel()
	// A click handler often needs the current zoom (decide marker detail, snap
	// precision). Every gesture must carry the LIVE camera, so reading e.Zoom
	// in a click handler is fresh — not a leftover from the last moveend.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnClick(p.PlacePin)))

	for _, sig := range []string{
		"$viaMapZoom=evt.detail.zoom", "$viaMapBearing=evt.detail.bearing", "$viaMapPitch=evt.detail.pitch",
	} {
		assert.Contains(t, html, sig, "a click must write the live camera signals")
	}
	for _, read := range []string{"zoom:_m.getZoom()", "bearing:_m.getBearing()", "pitch:_m.getPitch()"} {
		assert.Contains(t, html, read, "the click listener must read the camera at click time")
	}
	// Camera must be ADDITIVE — the click point (lng/lat) must still be carried,
	// not replaced by the camera fields.
	assert.Contains(t, html, "$viaMapLng=evt.detail.lng", "the click point must still be carried")
	assert.Contains(t, html, "$viaMapLat=evt.detail.lat", "the click point must still be carried")
}

func TestMap_OnFeatureClick_carriesLiveCamera(t *testing.T) {
	t.Parallel()
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnFeatureClick("countries", p.Picked)))

	assert.Contains(t, html, "$viaMapZoom=evt.detail.zoom",
		"a feature click must write the live camera signals")
	assert.Contains(t, html, "zoom:_m.getZoom()",
		"the feature-click listener must read the camera at click time")
}

func TestMap_OnMarkerClick_carriesLiveCameraInExpr(t *testing.T) {
	t.Parallel()
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnMarkerClick(p.Selected)))
	assert.Contains(t, html, "$viaMapZoom=evt.detail.zoom",
		"a marker click must write the live camera signals")
}

func TestMap_OnMarkerDragEnd_carriesLiveCameraInExpr(t *testing.T) {
	t.Parallel()
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnMarkerDragEnd(p.Moved)))
	assert.Contains(t, html, "$viaMapZoom=evt.detail.zoom",
		"a marker drag-end must write the live camera signals")
}

func TestMap_OnMapEvent_wiresAnyMapLibreEventToAGoAction(t *testing.T) {
	t.Parallel()
	// The typed handlers cover the common gestures; OnMapEvent is the escape
	// hatch so a dev is never blocked by a missing inbound event (dblclick,
	// contextmenu, …). It must carry the pointer position and the live camera.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnMapEvent("dblclick", p.PlacePin)))

	assert.Contains(t, html, "data-on:viamapevent0",
		"the container must listen for the dispatched generic event")
	assert.Contains(t, html, `_m.on("dblclick"`,
		"initJS must subscribe to the named MapLibre event")
	assert.Contains(t, html, "/_action/PlacePin",
		"the event must POST to the bound method's action endpoint")
	assert.Contains(t, html, "$viaMapLng=evt.detail.lng")
	assert.Contains(t, html, "$viaMapZoom=evt.detail.zoom",
		"a generic event must still carry the live camera")
	assert.Contains(t, html, "e.lngLat?e.lngLat.lng:0",
		"the detail must guard lngLat — some events have no pointer position")
}

func TestMap_OnMapEvent_distinctEventsRouteIndependently(t *testing.T) {
	t.Parallel()
	// dblclick and right-click must reach their own actions, not collide.
	p := &eventPage{}
	html := render(t, newEventMap(
		maplibre.OnMapEvent("dblclick", p.PlacePin),
		maplibre.OnMapEvent("contextmenu", p.Recenter),
	))
	assert.Contains(t, html, "data-on:viamapevent0")
	assert.Contains(t, html, "data-on:viamapevent1")
	assert.Contains(t, html, `_m.on("dblclick"`)
	assert.Contains(t, html, `_m.on("contextmenu"`)
	assert.Contains(t, html, "/_action/PlacePin")
	assert.Contains(t, html, "/_action/Recenter")
}

func TestMap_OnMapEvent_carriesCameraEvenForPointerlessEvents(t *testing.T) {
	t.Parallel()
	// zoomend/rotateend have no lngLat, but the camera is exactly what such an
	// event is about — it must still arrive.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnMapEvent("zoomend", p.Recenter)))
	assert.Contains(t, html, `_m.on("zoomend"`)
	assert.Contains(t, html, "$viaMapZoom=evt.detail.zoom",
		"a pointerless event must still carry the live camera")
}

func TestOnMapEvent_panicsOnNonBoundMethod(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		maplibre.NewMap(maplibre.OnMapEvent("dblclick", func(ctx *via.Ctx) {}))
	})
}

func TestInlinedIdentifiers_escapeSingleQuotesSoTheyCannotBreakOut(t *testing.T) {
	t.Parallel()
	// Layer ids / event names are inlined into JS next to single-quoted
	// literals. json.Marshal leaves a single quote raw, so jsString must escape
	// it (to ') — a malicious or odd id can't break out of the script.
	html := render(t, maplibre.NewMap(
		maplibre.WithElementID("m"),
		maplibre.WithFeatureHover("a');evil();//"),
	))
	assert.Contains(t, html, `a'`, "the single quote must be unicode-escaped")
	assert.NotContains(t, html, `'a');evil`, "the raw single-quoted breakout must not appear")
}

func TestMap_WithFeatureHover_highlightsHoveredFeatureClientSide(t *testing.T) {
	t.Parallel()
	// Hover-to-highlight is the signature interactive-map behavior. It must be
	// pure client JS (feature-state, no server round-trip) so it feels instant.
	html := render(t, maplibre.NewMap(maplibre.WithElementID("m"), maplibre.WithFeatureHover("zones")))

	assert.Contains(t, html, `.on('mousemove',"zones"`,
		"hovering a feature in the layer must be tracked")
	assert.Contains(t, html, `.on('mouseleave',"zones"`,
		"leaving the layer must clear the highlight")
	assert.Contains(t, html, "setFeatureState",
		"the highlight must be driven by MapLibre feature-state")
	assert.Contains(t, html, "{hover:true}",
		"the hovered feature must enter the hover state")
	assert.Contains(t, html, "{hover:false}",
		"the previously hovered feature must leave the hover state")
}

func TestMap_WithFeatureHover_emitsNoActionAttribute(t *testing.T) {
	t.Parallel()
	// Hover is client-only — it must NOT emit a data-on attribute, and above
	// all not a malformed empty one (datastar rejects data-on:= with
	// ValueRequired and freezes the page).
	html := render(t, maplibre.NewMap(maplibre.WithElementID("m"), maplibre.WithFeatureHover("zones")))

	assert.NotContains(t, html, "data-on:=",
		"a client-only handler must not emit an empty data-on attribute")
	assert.NotContains(t, html, `data-on:"`,
		"a client-only handler must not emit a malformed empty-event data-on attribute")
}

func TestMap_actionHandlerStillEmitsDataOn_afterJSOnlyHandlerSupport(t *testing.T) {
	t.Parallel()
	// The domEvent!="" guard that lets hover be attribute-less must NOT drop
	// real action handlers — OnClick must still wire its data-on.
	p := &eventPage{}
	html := render(t, newEventMap(
		maplibre.OnClick(p.PlacePin),
		maplibre.WithFeatureHover("zones"),
	))
	assert.Contains(t, html, "data-on:viamapclick",
		"a real action handler must still emit its data-on alongside a hover handler")
	assert.Contains(t, html, `.on('mousemove',"zones"`,
		"the hover handler must still wire its client JS")
}

func TestMap_bothHandlers_coexistOnOneMap(t *testing.T) {
	t.Parallel()
	// Registering click AND moveend on the same map must wire both — a
	// shared storage slot that the second handler clobbers would silently
	// drop one gesture.
	p := &eventPage{}
	html := render(t, newEventMap(
		maplibre.OnClick(p.PlacePin),
		maplibre.OnMoveEnd(p.Recenter),
	))
	assert.Contains(t, html, "data-on:viamapclick")
	assert.Contains(t, html, "data-on:viamapmove")
	assert.Contains(t, html, "/_action/PlacePin")
	assert.Contains(t, html, "/_action/Recenter")
	assert.Contains(t, html, ".on('click'")
	assert.Contains(t, html, ".on('moveend'")
}

func TestMap_OnMarkerClick_routesMarkerIdentityToAGoAction(t *testing.T) {
	t.Parallel()
	// Selecting a feature is the core of any pin-based app: a marker click must
	// reach a Go action carrying which marker (id) and where (lng/lat).
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnMarkerClick(p.Selected)))

	assert.Contains(t, html, "data-on:viamarkerclick",
		"the container must listen for the dispatched marker-click event")
	assert.Contains(t, html, "/_action/Selected",
		"the marker click must POST to the bound method's action endpoint")
	assert.Contains(t, html, "$viaMapId=evt.detail.id",
		"the clicked marker's id must be written to the shared signal")
	assert.Contains(t, html, "$viaMapLng=evt.detail.lng")
	assert.Contains(t, html, "$viaMapLat=evt.detail.lat")
}

func TestMap_OnMarkerDragEnd_routesDroppedPositionToAGoAction(t *testing.T) {
	t.Parallel()
	// Drag-to-reposition (move a vehicle, adjust a geofence) needs the dropped
	// marker's id and its new lng/lat to reach Go.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnMarkerDragEnd(p.Moved)))

	assert.Contains(t, html, "data-on:viamarkerdragend",
		"the container must listen for the dispatched drag-end event")
	assert.Contains(t, html, "/_action/Moved")
	assert.Contains(t, html, "$viaMapId=evt.detail.id")
	assert.Contains(t, html, "$viaMapLng=evt.detail.lng")
	assert.Contains(t, html, "$viaMapLat=evt.detail.lat")
}

func TestMap_AddMarker_attachesClickListenerOnlyWhenHandlerRegistered(t *testing.T) {
	t.Parallel()
	// The listener must dispatch the custom event the container routes, stop
	// propagation so the basemap OnClick doesn't also fire, and carry the
	// marker's own id + live position.
	frame := fireEventAction(t, "AddPin", nil, "setLngLat")
	assert.Contains(t, frame, "addEventListener('click'",
		"a registered marker-click handler must wire the marker element")
	assert.Contains(t, frame, "viamarkerclick",
		"the listener must dispatch the event the container listens for")
	assert.Contains(t, frame, "stopPropagation",
		"a marker click must not also trigger the basemap click handler")
	assert.Contains(t, frame, "getContainer()",
		"the event must be dispatched on the map container that carries data-on")
	assert.Contains(t, frame, `id:"car"`,
		"the dispatched detail must carry this marker's own id (bound to the detail, not just the registry key)")
	assert.Contains(t, frame, "getElement().style.cursor='pointer'",
		"a clickable marker must show a pointer cursor so it reads as clickable")
	assert.Contains(t, frame, "zoom:_e.m.getZoom()",
		"the marker-click detail must carry the live camera so e.Zoom is fresh")
}

func TestMap_AddMarker_attachesDragEndListenerWhenHandlerRegistered(t *testing.T) {
	t.Parallel()
	frame := fireEventAction(t, "AddPin", nil, "setLngLat")
	assert.Contains(t, frame, ".on('dragend'",
		"a registered drag-end handler must subscribe to the marker's dragend")
	assert.Contains(t, frame, "viamarkerdragend",
		"the drag-end listener must dispatch the event the container listens for")
	assert.Contains(t, frame, "zoom:_e.m.getZoom()",
		"the drag-end detail must carry the live camera so e.Zoom is fresh")
}

func TestMap_OnFeatureClick_routesClickedFeatureToAGoAction(t *testing.T) {
	t.Parallel()
	// Clicking a rendered shape (a country, a route, a zone) must tell Go WHICH
	// feature — by an identifying property — so a server-authoritative app can
	// look up that feature's data.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnFeatureClick("countries", p.Picked)))

	assert.Contains(t, html, "data-on:viafeatureclick0",
		"the container must listen for the dispatched feature-click event")
	assert.Contains(t, html, "/_action/Picked",
		"the feature click must POST to the bound method's action endpoint")
	assert.Contains(t, html, "$viaMapFeatureId=evt.detail.fid",
		"the clicked feature's id must be written to the shared signal")
	assert.Contains(t, html, "$viaMapLng=evt.detail.lng")
	assert.Contains(t, html, "$viaMapLat=evt.detail.lat")
	assert.Contains(t, html, `.on('click',"countries"`,
		"initJS must subscribe to layer-scoped clicks on the named layer")
	assert.Contains(t, html, "e.features",
		"the listener must read the clicked features under the cursor")
	assert.Contains(t, html, "f.properties",
		"the listener must read the clicked feature's properties")
	assert.Contains(t, html, `p["id"]`,
		"the default identifier property 'id' must be the property read from the feature")
	assert.Contains(t, html, ":f.id",
		"when the named property is absent the GeoJSON feature id must be the fallback")
}

func TestMap_OnFeatureClick_showsPointerCursorOnHover(t *testing.T) {
	t.Parallel()
	// A filled polygon or line gives no hint it's clickable. The pointer
	// cursor on hover is the standard map affordance — without it the feature
	// looks inert.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnFeatureClick("countries", p.Picked)))

	assert.Contains(t, html, `.on('mouseenter',"countries"`,
		"hovering a clickable layer must be detected")
	assert.Contains(t, html, `.on('mouseleave',"countries"`,
		"leaving the layer must be detected to restore the cursor")
	assert.Contains(t, html, "cursor='pointer'",
		"the cursor must become a pointer over a clickable feature")
	assert.Contains(t, html, "cursor=''",
		"the cursor must reset when the pointer leaves the feature")
	assert.Contains(t, html, `.on('click',"countries"`,
		"the click subscription must remain")
}

func TestMap_OnFeatureClick_honorsConfiguredIdProperty(t *testing.T) {
	t.Parallel()
	// Real datasets key features on their own property (iso code, FIPS, a DB
	// id) — not always a GeoJSON top-level id. The handler must read it.
	p := &eventPage{}
	html := render(t, newEventMap(maplibre.OnFeatureClick("countries", p.Picked, "iso")))
	assert.Contains(t, html, `p["iso"]`,
		"a configured idProperty must be the property read from the feature")
}

func TestMap_OnFeatureClick_distinctLayersRouteToDistinctActions(t *testing.T) {
	t.Parallel()
	// Two layers, two handlers: each must dispatch its own event to its own
	// action, or one layer's clicks would be swallowed by the other.
	p := &eventPage{}
	html := render(t, newEventMap(
		maplibre.OnFeatureClick("countries", p.Picked),
		maplibre.OnFeatureClick("cities", p.Selected),
	))
	assert.Contains(t, html, "data-on:viafeatureclick0")
	assert.Contains(t, html, "data-on:viafeatureclick1")
	assert.Contains(t, html, "/_action/Picked")
	assert.Contains(t, html, "/_action/Selected")
	assert.Contains(t, html, `.on('click',"countries"`)
	assert.Contains(t, html, `.on('click',"cities"`)
}

func TestOnFeatureClick_panicsOnNonBoundMethod(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		maplibre.NewMap(maplibre.OnFeatureClick("countries", func(ctx *via.Ctx) {}))
	})
}

func TestMap_Event_returnsClickedFeatureIdPostedFromTheBrowser(t *testing.T) {
	t.Parallel()
	// The selected feature's id must round-trip so the handler can look up its
	// server-side record.
	frame := fireEventAction(t, "Picked",
		map[string]any{"viaMapFeatureId": "country-DE", "viaMapLng": 10.25, "viaMapLat": 51.0},
		"gotFid")
	assert.Contains(t, frame, `"gotFid":"country-DE"`, "the clicked feature's id must reach the handler")
	assert.Contains(t, frame, `"gotLng":10.25`, "the click longitude must reach the handler")
}

func TestMap_AddMarker_skipsListenersWhenNoMarkerHandlerRegistered(t *testing.T) {
	t.Parallel()
	// A map with no marker handlers must not pay for listeners — fireMapAction
	// uses a page that registers none.
	frame := fireMapAction(t, "AddMarker", "setLngLat")
	assert.Contains(t, frame, "setLngLat",
		"the marker is still created — so the NotContains below is meaningful, not vacuous")
	assert.NotContains(t, frame, "viamarkerclick")
	assert.NotContains(t, frame, "viamarkerdragend")
	assert.NotContains(t, frame, "cursor='pointer'",
		"a marker with no click handler must not be dressed up as clickable")
}

func TestMap_withoutHandlers_emitsNoEventWiring(t *testing.T) {
	t.Parallel()
	// A purely server-driven map must not pay for listeners it never uses,
	// nor open an action endpoint the developer never wired.
	html := render(t, maplibre.NewMap(maplibre.WithElementID("m")))

	assert.NotContains(t, html, "viamapclick")
	assert.NotContains(t, html, "viamapmove")
}

func TestMap_eventSignals_areNotDatastarLocalSignals(t *testing.T) {
	t.Parallel()
	// Datastar never sends signals whose name starts with "_" to the server —
	// they are client-only "local" signals. If any inbound-event signal used a
	// leading underscore, every gesture would post empty data and the handler
	// would silently read zeros. Guard the wire keys against that whole class
	// of bug (the vt harness can't catch it — it injects signals directly into
	// the POST body, bypassing datastar's client-side filtering).
	p := &eventPage{}
	html := render(t, newEventMap(
		maplibre.OnClick(p.PlacePin),
		maplibre.OnMoveEnd(p.Recenter),
		maplibre.OnMarkerClick(p.Selected),
		maplibre.OnMarkerDragEnd(p.Moved),
	))
	assert.NotContains(t, html, "$_",
		"event signals must not start with '_' — datastar drops local signals before posting")

	// And the read side: every MapEvent form tag must be a sendable key too.
	for _, e := range reflect.VisibleFields(reflect.TypeOf(maplibre.MapEvent{})) {
		tag := e.Tag.Get("form")
		require.NotEmpty(t, tag, "every MapEvent field needs a form tag")
		assert.False(t, strings.HasPrefix(tag, "_"),
			"MapEvent.%s form key %q starts with '_' — datastar won't send it", e.Name, tag)
	}
}

func TestOnClick_panicsOnNonBoundMethod(t *testing.T) {
	t.Parallel()
	// Mirrors the on package: a closure or top-level func has no stable
	// action name, so the mistake must surface at construction, not silently.
	assert.Panics(t, func() {
		maplibre.NewMap(maplibre.OnClick(func(ctx *via.Ctx) {}))
	})
}

func TestOnMoveEnd_panicsOnNonBoundMethod(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		maplibre.NewMap(maplibre.OnMoveEnd(func(ctx *via.Ctx) {}))
	})
}

// fireEventAction boots the eventPage app, fires the named action with the
// given signals attached, and returns the frame once every needle appears.
func fireEventAction(t *testing.T, action string, sigs map[string]any, needles ...string) string {
	t.Helper()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[eventPage](app, "/")
	t.Cleanup(server.Close)

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	t.Cleanup(cancel)

	call := tc.Action(action)
	for k, v := range sigs {
		call = call.WithSignal(k, v)
	}
	require.Equal(t, 200, call.Fire())
	return vt.AwaitFrame(t, frames, 2*time.Second, needles...)
}

func TestMap_Event_returnsClickedCoordinatesPostedFromTheBrowser(t *testing.T) {
	t.Parallel()
	// The whole point: a click in the browser must round-trip into the typed
	// lng/lat the Go handler reads via Map.Event.
	frame := fireEventAction(t, "PlacePin",
		map[string]any{"viaMapLng": -122.42, "viaMapLat": 37.77},
		"gotLng")
	// Bind value to its signal key so a stray "37.77" elsewhere in the frame
	// can't pass the test for the wrong reason.
	assert.Contains(t, frame, `"gotLng":-122.42`, "the clicked longitude must reach the handler")
	assert.Contains(t, frame, `"gotLat":37.77`, "the clicked latitude must reach the handler")
}

func TestMap_Event_returnsClickedMarkerIdentityPostedFromTheBrowser(t *testing.T) {
	t.Parallel()
	// The marker's id must round-trip so the handler knows which pin was
	// selected, not just where it sits.
	frame := fireEventAction(t, "Selected",
		map[string]any{"viaMapId": "vehicle-7", "viaMapLng": 13.4, "viaMapLat": 52.5},
		"gotId")
	assert.Contains(t, frame, `"gotId":"vehicle-7"`, "the selected marker's id must reach the handler")
	assert.Contains(t, frame, `"gotLng":13.4`, "the marker's longitude must reach the handler")
	assert.Contains(t, frame, `"gotLat":52.5`, "the marker's latitude must reach the handler")
}

func TestMap_Event_returnsSettledViewportPostedFromTheBrowser(t *testing.T) {
	t.Parallel()
	// Viewport-driven loading depends on the bounding box and zoom surviving
	// the round-trip into Map.Event.
	frame := fireEventAction(t, "Recenter",
		map[string]any{
			"viaMapZoom": 9.5, "viaMapBearing": 33.5, "viaMapPitch": 12.5,
			"viaMapW": -10.5, "viaMapS": 40.25, "viaMapE": 5.75, "viaMapN": 55.25,
		},
		"gotZoom")
	assert.Contains(t, frame, `"gotZoom":9.5`, "the settled zoom must reach the handler")
	assert.Contains(t, frame, `"gotBearing":33.5`, "the bearing must reach the handler")
	assert.Contains(t, frame, `"gotPitch":12.5`, "the pitch must reach the handler")
	assert.Contains(t, frame, `"gotW":-10.5`, "the west bound must reach the handler")
	assert.Contains(t, frame, `"gotS":40.25`, "the south bound must reach the handler")
	assert.Contains(t, frame, `"gotE":5.75`, "the east bound must reach the handler")
	assert.Contains(t, frame, `"gotN":55.25`, "the north bound must reach the handler")
}
