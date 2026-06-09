package maplibre_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapAPI_AddMarker_emitsMarkerAtLngLat(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "AddMarker",
		"new maplibregl.Marker(", "setLngLat([-122.42,37.77])", "addTo")
}

func TestMapAPI_AddMarker_carriesColorOption(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "AddMarkerColor", `"color":"#ff0000"`)
}

func TestMapAPI_AddMarker_popupTextUsesSafeSetText(t *testing.T) {
	t.Parallel()
	// setText creates a DOM text node — the XSS-safe path for user content.
	fireMapAction(t, "AddMarkerPopupText", "new maplibregl.Popup(", ".setText(", "Hello there")
}

func TestMapAPI_AddMarker_popupHTMLUsesSetHTML(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "AddMarkerPopupHTML", ".setHTML(")
}

func TestMapAPI_AddMarker_popupTextAndHTMLAreMutuallyExclusiveLastWins(t *testing.T) {
	t.Parallel()
	// Both set in one call (HTML then Text): the later one must win and the
	// earlier must be fully dropped, so the popup can't render twice.
	frame := fireMapAction(t, "AddMarkerPopupLastWins", "setLngLat")
	assert.Contains(t, frame, `.setText("text")`, "the later PopupText must win")
	assert.NotContains(t, frame, ".setHTML(", "the earlier PopupHTML must be cleared")
}

func TestMapAPI_AddMarker_escapesScriptBreakoutInPopupText(t *testing.T) {
	t.Parallel()
	// json.Marshal HTML-escapes `<` to <, so a popup string carrying a
	// </script><img onerror> breakout can't terminate the SSE-delivered
	// script nor inject a raw tag. (The frame's own trailing </script> is
	// datastar's script wrapper, not our content — so assert on the payload.)
	frame := fireMapAction(t, "AddMarkerXSS", ".setText(")
	assert.Contains(t, frame, `</script>`,
		"the content's </script> must be unicode-escaped by the JSON encoder")
	assert.NotContains(t, frame, "</script><img",
		"the breakout sequence must not survive as raw markup")
	assert.NotContains(t, frame, "<img",
		"the injected tag must be escaped, never emitted raw")
}

func TestMapAPI_AddMarker_escapesQuoteInMarkerID(t *testing.T) {
	t.Parallel()
	// A quote in the id must be JSON-escaped so it stays a valid string key
	// and can't break out of the registry index expression.
	frame := fireMapAction(t, "AddMarkerQuoteID", `markers[`)
	assert.Contains(t, frame, `a\"b`,
		"a quote in the marker id must be escaped inside the JS string literal")
}

func TestMapAPI_AddMarker_removesSameIDBeforeAddingToAvoidStacking(t *testing.T) {
	t.Parallel()
	// The replace-first guard lets a stream re-emit a marker per tick without
	// stacking duplicates: the remove must precede the new Marker construction.
	frame := fireMapAction(t, "AddMarker", `_e.markers["a"]`, ".remove()")
	assert.Less(t, strings.Index(frame, ".remove()"), strings.Index(frame, "new maplibregl.Marker"),
		"AddMarker must remove an existing same-id marker before constructing the replacement")
}

func TestMapAPI_MoveMarker_repositionsWithoutRecreating(t *testing.T) {
	t.Parallel()
	frame := fireMapAction(t, "MoveMarker", "setLngLat([3,4])", "if(_mk)")
	assert.NotContains(t, frame, "new maplibregl.Marker",
		"MoveMarker must reuse the existing marker, not construct a new one")
}

func TestMapAPI_RemoveMarker_guardsAgainstMissingMarker(t *testing.T) {
	t.Parallel()
	// The if(_mk) guard makes removing an absent id a no-op rather than a
	// throw on undefined.
	fireMapAction(t, "RemoveMarker", "if(_mk)", ".remove()")
}

func TestMapAPI_RemoveMarker_removesAndDropsFromRegistry(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "RemoveMarker", ".remove()", "delete")
}

func TestMapAPI_ClearMarkers_removesEveryMarker(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "ClearMarkers", "Object.keys", ".remove()")
}

func TestWithMarker_placesAStaticMarkerAtConstruction(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(
		maplibre.WithElementID("m"),
		maplibre.WithMarker("home", maplibre.At(-0.1, 51.5),
			maplibre.Color("#ff0000"), maplibre.PopupText("Home")),
	))

	assert.Contains(t, html, "new maplibregl.Marker(", "the static marker must be constructed")
	assert.Contains(t, html, "setLngLat([-0.1,51.5])", "at its [lng, lat]")
	assert.Contains(t, html, `"color":"#ff0000"`, "honoring its MarkerOptions")
	assert.Contains(t, html, `.setText("Home")`, "including its popup")
	assert.Contains(t, html, "addTo(_e.m)", "and added to the map")
	assert.Contains(t, html, `_e.markers["home"]=`, "and stored under its id for later MoveMarker/RemoveMarker")
}

func TestWithMarker_rendersAfterTheRegistryEntryExists(t *testing.T) {
	t.Parallel()
	// markerScript looks up window.__viaMaps[seq] as _e; that slot is assigned
	// at the end of init, so a construction marker emitted before it would
	// no-op (the `if(!_e)return` guard). It must come AFTER the assignment.
	html := render(t, maplibre.NewMap(
		maplibre.WithElementID("m"),
		maplibre.WithMarker("home", maplibre.At(-0.1, 51.5)),
	))
	registry := strings.Index(html, "={m:_m,ro:_ro,markers:{}}")
	marker := strings.Index(html, "new maplibregl.Marker(")
	require.NotEqual(t, -1, registry, "registry entry must be assigned")
	require.NotEqual(t, -1, marker, "marker must be constructed")
	assert.Less(t, registry, marker,
		"the registry entry must be assigned before the construction marker runs")
}

func TestWithMarker_multipleStaticMarkersEachRender(t *testing.T) {
	t.Parallel()
	// Several fixed pins (one bare, one styled) must all render without
	// clobbering each other's id slot.
	html := render(t, maplibre.NewMap(
		maplibre.WithElementID("m"),
		maplibre.WithMarker("a", maplibre.At(0, 0)),
		maplibre.WithMarker("b", maplibre.At(1, 1), maplibre.Color("#00ff00")),
	))
	assert.Equal(t, 2, strings.Count(html, "new maplibregl.Marker("),
		"each static marker must be constructed")
	assert.Contains(t, html, `_e.markers["a"]=`)
	assert.Contains(t, html, `_e.markers["b"]=`)
}

func TestWithMarker_adjacentMarkersDoNotFuseIntoACall(t *testing.T) {
	t.Parallel()
	// Each construction marker is emitted as a self-invoking IIFE. Without a
	// statement terminator between them, two adjacent IIFEs read as
	// `})()(function(){...` — JS parses the second IIFE as an argument to a
	// CALL on the first IIFE's return value (undefined), throwing a TypeError
	// at runtime and aborting every marker after the first. The emitted JS
	// must keep the marker scripts as separate statements.
	html := render(t, maplibre.NewMap(
		maplibre.WithElementID("m"),
		maplibre.WithMarker("a", maplibre.At(0, 0)),
		maplibre.WithMarker("b", maplibre.At(1, 1)),
	))
	assert.NotContains(t, html, "})()(function(){",
		"adjacent marker IIFEs must not fuse into a call expression")
}

func TestWithMarker_honorsMarkerClickWiring(t *testing.T) {
	t.Parallel()
	// A construction marker on a map with OnMarkerClick must get the same
	// click listener as a runtime AddMarker would — markerScript reads
	// m.markerClick at render time, after all options applied.
	p := &eventPage{}
	html := render(t, maplibre.NewMap(
		maplibre.WithElementID("m"),
		maplibre.OnMarkerClick(p.Selected),
		maplibre.WithMarker("home", maplibre.At(-0.1, 51.5)),
	))
	assert.Contains(t, html, "viamarkerclick",
		"a construction marker must dispatch the marker-click event when OnMarkerClick is set")
	assert.Contains(t, html, "getElement().style.cursor='pointer'",
		"and read as clickable")
}

// staticMarkerPage has NO OnConnect — the marker is declared at construction.
type staticMarkerPage struct {
	Map *maplibre.Map
}

func (p *staticMarkerPage) OnInit(ctx *via.Ctx) error {
	if p.Map == nil {
		p.Map = maplibre.NewMap(
			maplibre.WithElementID("m"),
			maplibre.WithMarker("home", maplibre.At(-0.1, 51.5), maplibre.PopupText("Home")),
		)
	}
	return nil
}

func (p *staticMarkerPage) View(ctx *via.CtxR) h.H { return p.Map.Mount() }

func TestWithMarker_needsNoOnConnectToAppear(t *testing.T) {
	t.Parallel()
	// The whole point: a fixed marker must show on first paint, with no
	// OnConnect and no SSE round-trip — so it's in the GET response body.
	app := via.New(via.WithPlugins(maplibre.Plugin()))
	server := vt.Serve(t, app)
	via.Mount[staticMarkerPage](app, "/")
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, string(body), "new maplibregl.Marker(",
		"the static marker must be present in the initial HTML, not an SSE frame")
	assert.Contains(t, string(body), `_e.markers["home"]=`)
}
