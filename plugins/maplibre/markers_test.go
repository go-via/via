package maplibre_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
