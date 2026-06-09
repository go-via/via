package maplibre_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
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
	html := render(t, maplibre.NewMap(maplibre.WithCenter(maplibre.At(-122.42, 37.77))))
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
	html := render(t, maplibre.NewMap(maplibre.WithMaxBounds(maplibre.Bounds{West: -10, South: 40, East: 5, North: 55})))
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

// twoMapPage mounts two independent maps on one page, each with its own click
// handler, so we can prove they don't clobber each other's registry slot or
// route a gesture to the wrong action.
type twoMapPage struct {
	A *maplibre.Map
	B *maplibre.Map
}

func (p *twoMapPage) OnInit(ctx *via.Ctx) error {
	if p.A == nil {
		p.A = maplibre.NewMap(maplibre.WithElementID("mapA"), maplibre.OnClick(p.ClickA))
		p.B = maplibre.NewMap(maplibre.WithElementID("mapB"), maplibre.OnClick(p.ClickB))
	}
	return nil
}

func (p *twoMapPage) View(ctx *via.CtxR) h.H {
	return h.Body(p.A.Mount(), p.B.Mount())
}

func (p *twoMapPage) ClickA(ctx *via.Ctx) {
	e := p.A.Event(ctx)
	ctx.Patch.Signals(map[string]any{"clicked": "A", "gotLng": e.Lng})
}

func (p *twoMapPage) ClickB(ctx *via.Ctx) {
	e := p.B.Event(ctx)
	ctx.Patch.Signals(map[string]any{"clicked": "B", "gotLng": e.Lng})
}

// renderTwoMapPage boots a one-page app with two maps and returns the rendered
// HTML plus the live server for action firing.
func renderTwoMapPage(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	app := via.New(via.WithPlugins(maplibre.Plugin()))
	server := vt.Serve(t, app)
	via.Mount[twoMapPage](app, "/")
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	return string(body), server
}

func TestTwoMaps_eachGetsItsOwnContainerAndRegistrySlot(t *testing.T) {
	t.Parallel()
	html, _ := renderTwoMapPage(t)

	assert.Contains(t, html, `id="mapA"`)
	assert.Contains(t, html, `id="mapB"`)

	// Two distinct registry slots — if both maps wrote the same slot, the
	// second init would clobber the first's instance + observer.
	slots := regexp.MustCompile(`__viaMaps\[(\d+)\]=\{m:_m`).FindAllStringSubmatch(html, -1)
	require.Len(t, slots, 2, "each map must register exactly one slot")
	assert.NotEqual(t, slots[0][1], slots[1][1],
		"the two maps must occupy different registry slots")

	// Two independent map constructors on the one page.
	assert.Equal(t, 2, len(regexp.MustCompile(`new maplibregl\.Map\(`).FindAllString(html, -1)),
		"each map must construct its own MapLibre instance")
}

func TestTwoMaps_clicksRouteToTheirOwnActions(t *testing.T) {
	t.Parallel()
	html, _ := renderTwoMapPage(t)
	// Both containers carry the same custom-event name, but each wires it to
	// its OWN action — the event is dispatched on its own container and never
	// crosses to the sibling (non-bubbling), so this is safe.
	assert.Contains(t, html, "/_action/ClickA")
	assert.Contains(t, html, "/_action/ClickB")
	assert.Equal(t, 2, len(regexp.MustCompile(`data-on:viamapclick`).FindAllString(html, -1)),
		"each container must carry its own click listener")
}

func TestTwoMaps_eachActionReadsThePostedGesture(t *testing.T) {
	t.Parallel()
	_, server := renderTwoMapPage(t)

	tcA := vt.NewClient(t, server, "/")
	framesA, cancelA := tcA.SSEReady()
	t.Cleanup(cancelA)
	require.Equal(t, http.StatusOK,
		tcA.Action("ClickA").WithSignal("viaMapLng", -50.5).Fire())
	frameA := vt.AwaitFrame(t, framesA, 2*time.Second, "clicked")
	assert.Contains(t, frameA, `"clicked":"A"`, "map A's click must run map A's action")
	assert.Contains(t, frameA, `"gotLng":-50.5`, "map A's action must read the posted gesture")

	require.Equal(t, http.StatusOK,
		tcA.Action("ClickB").WithSignal("viaMapLng", 12.5).Fire())
	frameB := vt.AwaitFrame(t, framesA, 2*time.Second, `"clicked":"B"`)
	assert.Contains(t, frameB, `"gotLng":12.5`, "map B's action must read its own posted gesture")
}
