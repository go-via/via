package maplibre_test

import (
	"strings"
	"testing"

	"github.com/go-via/via/plugins/maplibre"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// render returns the HTML produced by a map's Mount node.
func render(t *testing.T, m *maplibre.Map) string {
	t.Helper()
	var sb strings.Builder
	require.NoError(t, m.Mount().Render(&sb))
	return sb.String()
}

func TestNewMap_assignsAutoIDWhenUnset(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap())
	assert.Contains(t, html, `id="maplibre-`,
		"a map without WithElementID must auto-generate an id matching `maplibre-<seq>`")
}

func TestNewMap_autoIDsAreUniqueAcrossMaps(t *testing.T) {
	t.Parallel()
	// Two maps on one page must land in distinct registry slots, else the
	// second init clobbers the first's instance + observer.
	a := render(t, maplibre.NewMap())
	b := render(t, maplibre.NewMap())
	assert.NotEqual(t, a, b,
		"two auto-id maps must render with different seq numbers")
}

func TestMap_Mount_rendersContainerWithDimensionsAndMorphGuard(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithDimensions("80%", "500px")))

	assert.Contains(t, html, "width:80%")
	assert.Contains(t, html, "height:500px")
	assert.Contains(t, html, "data-ignore-morph",
		"the container must survive datastar morphing or the WebGL canvas is wiped")
}

func TestMap_Mount_emitsConstructorAndRegistersInstance(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithElementID("m1")))

	assert.Contains(t, html, "new maplibregl.Map(",
		"Mount must emit the MapLibre constructor")
	assert.Contains(t, html, `"container":"m1"`,
		"the constructor must target the container by id")
	assert.Contains(t, html, "ResizeObserver",
		"a ResizeObserver keeps the map sized to its container")
	assert.Contains(t, html, "__viaMaps",
		"the instance must be parked in the registry for runtime ops")
}

func TestMap_defaultStyleIsDemotiles(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap())
	assert.Contains(t, html, "https://demotiles.maplibre.org/style.json",
		"the zero-config default must be the no-key demo style")
}

func TestMap_WithStyle_overridesStyleURL(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithStyle("https://tiles.example/s.json")))
	assert.Contains(t, html, `"style":"https://tiles.example/s.json"`)
	assert.NotContains(t, html, "demotiles.maplibre.org",
		"the default style must not also appear when overridden")
}

func TestMap_WithCenter_emitsLngLatOrder(t *testing.T) {
	t.Parallel()
	// MapLibre coordinates are [lng, lat] — longitude FIRST. Asserting the
	// exact pair order guards the single most common MapLibre bug.
	html := render(t, maplibre.NewMap(maplibre.WithCenter(-122.42, 37.77)))
	assert.Contains(t, html, `"center":[-122.42,37.77]`,
		"center must serialize as [lng, lat], not [lat, lng]")
}

func TestMap_WithZoomPitchBearing_emitsCameraOptions(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(
		maplibre.WithZoom(11),
		maplibre.WithPitch(45),
		maplibre.WithBearing(30),
	))
	assert.Contains(t, html, `"zoom":11`)
	assert.Contains(t, html, `"pitch":45`)
	assert.Contains(t, html, `"bearing":30`)
}

func TestMap_WithMaxBounds_emitsSouthWestNorthEast(t *testing.T) {
	t.Parallel()
	// maxBounds is [[west,south],[east,north]] = [sw, ne].
	html := render(t, maplibre.NewMap(maplibre.WithMaxBounds(-10, 40, 5, 55)))
	assert.Contains(t, html, `"maxBounds":[[-10,40],[5,55]]`)
}

func TestMap_WithoutAttribution_emitsAttributionControlFalse(t *testing.T) {
	t.Parallel()
	// The valid disable value is `false`; `true` is not a legal value.
	html := render(t, maplibre.NewMap(maplibre.WithoutAttribution()))
	assert.Contains(t, html, `"attributionControl":false`)
	assert.NotContains(t, html, `"attributionControl":true`)
}

func TestMap_WithoutInteraction_emitsInteractiveFalse(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithoutInteraction()))
	assert.Contains(t, html, `"interactive":false`)
}

func TestMap_Controls_addControlForEachKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opt    maplibre.MapOption
		needle string
	}{
		{"navigation", maplibre.WithNavigationControl(), "new maplibregl.NavigationControl("},
		{"scale", maplibre.WithScaleControl(), "new maplibregl.ScaleControl("},
		{"geolocate", maplibre.WithGeolocateControl(), "new maplibregl.GeolocateControl("},
		{"fullscreen", maplibre.WithFullscreenControl(), "new maplibregl.FullscreenControl("},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			html := render(t, maplibre.NewMap(tt.opt))
			assert.Contains(t, html, ".addControl("+tt.needle)
		})
	}
}

func TestMap_WithNavigationControl_honorsPosition(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithNavigationControl(maplibre.BottomLeft)))
	assert.Contains(t, html, `"bottom-left"`,
		"a control position must be passed as the second addControl argument")
}

func TestMap_WithNavigationControl_omitsPositionArgWhenUnset(t *testing.T) {
	t.Parallel()
	// No position arg => MapLibre's default (top-right). The call must end at
	// the constructor, with no trailing position string.
	html := render(t, maplibre.NewMap(maplibre.WithNavigationControl()))
	assert.Contains(t, html, "addControl(new maplibregl.NavigationControl({}));",
		"omitting a position must emit a single-argument addControl call")
}

func TestMap_WithHash_emitsHashOption(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithHash()))
	assert.Contains(t, html, `"hash":true`)
}

func TestMap_WithZoomRange_emitsMinAndMaxZoom(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithZoomRange(5, 18)))
	assert.Contains(t, html, `"minZoom":5`)
	assert.Contains(t, html, `"maxZoom":18`)
}

func TestMap_WithGeoJSONSourceAndLayer_addInsideLoad(t *testing.T) {
	t.Parallel()
	// Construction-time addSource/addLayer must run inside map.on('load'),
	// else MapLibre throws "Style is not done loading".
	m := maplibre.NewMap(
		maplibre.WithGeoJSONSource("pts", maplibre.FeatureCollection()),
		maplibre.WithLayer(maplibre.CircleLayer("dots", "pts")),
	)
	html := render(t, m)
	assert.Contains(t, html, ".on('load'",
		"sources and layers must be deferred to the load event")
	assert.Contains(t, html, ".addSource(")
	assert.Contains(t, html, ".addLayer(")
}

func TestMap_WithMapOption_escapeHatchPassesArbitraryConstructorKeys(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithMapOption("renderWorldCopies", false)))
	assert.Contains(t, html, `"renderWorldCopies":false`)
}

func TestMap_WithClass_addsContainerClasses(t *testing.T) {
	t.Parallel()
	html := render(t, maplibre.NewMap(maplibre.WithClass("rounded", "shadow")))
	assert.Contains(t, html, `class="rounded shadow"`)
}

func TestMap_Mount_escapesScriptBreakoutInInlineInitScript(t *testing.T) {
	t.Parallel()
	// The init script is inline in the page <script>; a constructor-option
	// value carrying </script><b> must be unicode-escaped by json.Marshal so
	// it can't close the script tag or inject markup.
	html := render(t, maplibre.NewMap(maplibre.WithMapOption("k", "</script><b>x</b>")))
	assert.Contains(t, html, `</script>`,
		"the option value's </script> must be unicode-escaped by json.Marshal")
	assert.NotContains(t, html, "<b>x</b>",
		"the injected markup must never appear raw in the document")
}

func TestWithElementID_panicsOnWhitespace(t *testing.T) {
	t.Parallel()
	// A whitespaced id is valid HTML5 but unaddressable by `#id` CSS/JS.
	assert.Panics(t, func() { maplibre.WithElementID("a b") })
}

func TestWithClass_panicsOnWhitespaceInSingleClass(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { maplibre.WithClass("a b") })
}
