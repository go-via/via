package maplibre_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	var server *httptest.Server
	app := via.New(via.WithPlugins(maplibre.Plugin()), via.WithTestServer(&server))
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
