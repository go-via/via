package maplibre_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-via/via/plugins/maplibre"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestPoint_emitsLngLatGeometry(t *testing.T) {
	t.Parallel()
	assert.JSONEq(t, `{"type":"Point","coordinates":[-122.4,37.8]}`,
		jsonOf(t, maplibre.Point(-122.4, 37.8)))
}

func TestLineString_emitsCoordinateArray(t *testing.T) {
	t.Parallel()
	got := maplibre.LineString([][]float64{{0, 0}, {1, 1}})
	assert.JSONEq(t, `{"type":"LineString","coordinates":[[0,0],[1,1]]}`, jsonOf(t, got))
}

func TestPointFeature_wrapsGeometryWithProperties(t *testing.T) {
	t.Parallel()
	got := maplibre.PointFeature(1, 2, map[string]any{"name": "x"})
	assert.JSONEq(t,
		`{"type":"Feature","geometry":{"type":"Point","coordinates":[1,2]},"properties":{"name":"x"}}`,
		jsonOf(t, got))
}

func TestFeature_defaultsNilPropertiesToEmptyObject(t *testing.T) {
	t.Parallel()
	got := maplibre.Feature(maplibre.Point(0, 0), nil)
	assert.Contains(t, jsonOf(t, got), `"properties":{}`,
		"a nil props must serialize as {} so the Feature stays valid GeoJSON")
}

func TestFeatureCollection_wrapsFeatures(t *testing.T) {
	t.Parallel()
	got := maplibre.FeatureCollection(maplibre.PointFeature(1, 2, nil))
	assert.Contains(t, jsonOf(t, got), `"type":"FeatureCollection"`)
	assert.Contains(t, jsonOf(t, got), `"features":[`)
}

func TestFeatureCollection_emptyIsNonNullFeaturesArray(t *testing.T) {
	t.Parallel()
	assert.Contains(t, jsonOf(t, maplibre.FeatureCollection()), `"features":[]`,
		"an empty collection must emit [] not null, so setData has a valid value")
}

func TestWithGeoJSONSource_GenerateFeatureIDs_emitsGenerateId(t *testing.T) {
	t.Parallel()
	// Feature-state (hover highlighting, selection) targets features by id.
	// GeoJSON features often lack ids; generateId:true makes MapLibre assign
	// them so setFeatureState has something to address.
	html := render(t, maplibre.NewMap(
		maplibre.WithElementID("m"),
		maplibre.WithGeoJSONSource("zones", maplibre.FeatureCollection(), maplibre.GenerateFeatureIDs()),
	))
	assert.Contains(t, html, `"generateId":true`,
		"GenerateFeatureIDs must set MapLibre's generateId on the source")
	// Additive — generateId augments the source spec, it doesn't replace it.
	assert.Contains(t, html, `"type":"geojson"`,
		"the source must still be a geojson source")
	assert.Contains(t, html, "addSource",
		"the source must still be added")
}

func TestWithGeoJSONSource_withoutOption_omitsGenerateId(t *testing.T) {
	t.Parallel()
	// The default must not silently turn on generateId — a dev who supplies
	// their own stable feature ids must not have them overwritten.
	html := render(t, maplibre.NewMap(
		maplibre.WithElementID("m"),
		maplibre.WithGeoJSONSource("zones", maplibre.FeatureCollection()),
	))
	assert.NotContains(t, html, "generateId",
		"a source without GenerateFeatureIDs must not emit generateId")
}

func TestCircleLayer_setsTypeSourceAndNestedPaint(t *testing.T) {
	t.Parallel()
	got := maplibre.CircleLayer("dots", "pts",
		maplibre.Paint("circle-radius", 6), maplibre.Paint("circle-color", "#e55"))
	js := jsonOf(t, got)
	assert.Contains(t, js, `"type":"circle"`)
	assert.Contains(t, js, `"source":"pts"`)
	assert.Contains(t, js, `"circle-radius":6`)
	assert.Contains(t, js, `"circle-color":"#e55"`)
}

func TestSymbolLayer_putsTextFieldUnderLayout(t *testing.T) {
	t.Parallel()
	got := maplibre.SymbolLayer("labels", "pts",
		maplibre.Layout("text-field", []any{"get", "name"}))
	assert.Contains(t, jsonOf(t, got), `"layout":{"text-field":["get","name"]}`)
}

func TestMapAPI_SetGeoJSON_setsSourceDataGuardedByStyleReady(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "SetGeoJSON",
		"__viaMapReady", `getSource("pts")`, ".setData(", `"FeatureCollection"`)
}

func TestMapAPI_AddGeoJSONSource_addsGeojsonTypedSourceIdempotently(t *testing.T) {
	t.Parallel()
	// The if(!getSource) guard makes a re-add a no-op rather than throwing on
	// a duplicate source id — so a reconnect can safely re-run it.
	fireMapAction(t, "AddSource", `if(!_m.getSource("pts"))`, ".addSource(", `"type":"geojson"`)
}

func TestMapAPI_AddLayer_addsLayerGuardedByExistence(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "AddCircleLayer", ".addLayer(", `"type":"circle"`, "getLayer")
}

func TestMapAPI_SetPaintProperty_emitsPaintUpdate(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "SetPaint", `setPaintProperty("dots","circle-color","#00f")`)
}

func TestMapAPI_SetLayerVisibility_togglesVisibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action string
		needle string
	}{
		{"show", "ShowLayer", "'visibility',\"visible\""},
		{"hide", "HideLayer", "'visibility',\"none\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fireMapAction(t, tt.action, "setLayoutProperty", tt.needle)
		})
	}
}

func TestMapAPI_RemoveLayerAndSource_removesLayerBeforeSource(t *testing.T) {
	t.Parallel()
	// removeSource throws if a layer still references it, so order matters.
	frame := fireMapAction(t, "RemoveLayerSource", "removeLayer", "removeSource")
	assert.Less(t, strings.Index(frame, "removeLayer"), strings.Index(frame, "removeSource"),
		"removeLayer must be emitted before removeSource")
}

func TestMapAPI_SetPaintProperty_returnsErrorOnUnmarshallableValue(t *testing.T) {
	t.Parallel()
	// A channel can't be JSON-encoded: the method returns the marshal error
	// (no broken script is emitted), which the default handler surfaces as a
	// toast carrying the package-prefixed message.
	frame := fireMapAction(t, "SetPaintBad", "maplibre: marshal")
	assert.NotContains(t, frame, "setPaintProperty",
		"a marshal failure must abort before emitting the paint script")
}
