package maplibre_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

// mapActionPage binds one Map and exposes every runtime method as an action,
// so the runtime tests can fire one and assert on the emitted SSE frame.
type mapActionPage struct {
	Map *maplibre.Map
}

func (p *mapActionPage) OnInit(ctx *via.Ctx) error {
	if p.Map == nil {
		p.Map = maplibre.NewMap(maplibre.WithElementID("m"))
	}
	return nil
}

func (p *mapActionPage) View(ctx *via.CtxR) h.H {
	if p.Map == nil {
		return h.Div()
	}
	return p.Map.Mount()
}

// Camera
func (p *mapActionPage) FlyTo(ctx *via.Ctx)     { p.Map.FlyTo(ctx, maplibre.At(-122.42, 37.77), 12) }
func (p *mapActionPage) EaseTo(ctx *via.Ctx)    { p.Map.EaseTo(ctx, maplibre.At(2.35, 48.85), 9) }
func (p *mapActionPage) JumpTo(ctx *via.Ctx)    { p.Map.JumpTo(ctx, maplibre.At(139.69, 35.69), 10) }
func (p *mapActionPage) SetCenter(ctx *via.Ctx) { p.Map.SetCenter(ctx, maplibre.At(-0.12, 51.5)) }
func (p *mapActionPage) SetZoom(ctx *via.Ctx)   { p.Map.SetZoom(ctx, 7) }
func (p *mapActionPage) SetPitch(ctx *via.Ctx)  { p.Map.SetPitch(ctx, 60) }
func (p *mapActionPage) SetBearing(ctx *via.Ctx) {
	p.Map.SetBearing(ctx, 90)
}
func (p *mapActionPage) FitBounds(ctx *via.Ctx) {
	p.Map.FitBounds(ctx, maplibre.Bounds{West: -10, South: 40, East: 5, North: 55})
}

// Markers
func (p *mapActionPage) AddMarker(ctx *via.Ctx) {
	p.Map.AddMarker(ctx, "a", maplibre.At(-122.42, 37.77))
}
func (p *mapActionPage) AddMarkerColor(ctx *via.Ctx) {
	p.Map.AddMarker(ctx, "a", maplibre.At(1, 2), maplibre.Color("#ff0000"))
}
func (p *mapActionPage) AddMarkerPopupText(ctx *via.Ctx) {
	p.Map.AddMarker(ctx, "a", maplibre.At(1, 2), maplibre.PopupText("Hello there"))
}
func (p *mapActionPage) AddMarkerPopupHTML(ctx *via.Ctx) {
	p.Map.AddMarker(ctx, "a", maplibre.At(1, 2), maplibre.PopupHTML(h.B(h.T("trusted"))))
}
func (p *mapActionPage) AddMarkerPopupLastWins(ctx *via.Ctx) {
	p.Map.AddMarker(ctx, "a", maplibre.At(1, 2),
		maplibre.PopupHTML(h.B(h.T("html"))), maplibre.PopupText("text"))
}
func (p *mapActionPage) AddMarkerXSS(ctx *via.Ctx) {
	p.Map.AddMarker(ctx, "a", maplibre.At(1, 2), maplibre.PopupText(`</script><img src=x onerror="alert(1)">`))
}
func (p *mapActionPage) AddMarkerQuoteID(ctx *via.Ctx) {
	p.Map.AddMarker(ctx, `a"b`, maplibre.At(1, 2))
}
func (p *mapActionPage) MoveMarker(ctx *via.Ctx)   { p.Map.MoveMarker(ctx, "a", maplibre.At(3, 4)) }
func (p *mapActionPage) RemoveMarker(ctx *via.Ctx) { p.Map.RemoveMarker(ctx, "a") }
func (p *mapActionPage) ClearMarkers(ctx *via.Ctx) { p.Map.ClearMarkers(ctx) }

// Popups (dialogs)
func (p *mapActionPage) ShowPopup(ctx *via.Ctx) {
	p.Map.ShowPopup(ctx, "info", maplibre.At(-122.42, 37.77), "Hello there")
}
func (p *mapActionPage) ShowPopupHTML(ctx *via.Ctx) {
	p.Map.ShowPopupHTML(ctx, "info", maplibre.At(1, 2), h.B(h.T("trusted")))
}
func (p *mapActionPage) ShowPopupStructured(ctx *via.Ctx) {
	p.Map.ShowPopupHTML(ctx, "info", maplibre.At(1, 2),
		h.Div(h.H3(h.T("Title")), h.P(h.T("body text"))))
}
func (p *mapActionPage) ShowPopupEscaped(ctx *via.Ctx) {
	p.Map.ShowPopupHTML(ctx, "info", maplibre.At(1, 2), h.Div(h.T("</script> & <b>evil")))
}
func (p *mapActionPage) ShowPopupNilHTML(ctx *via.Ctx) {
	p.Map.ShowPopupHTML(ctx, "info", maplibre.At(1, 2), nil)
}
func (p *mapActionPage) ShowPopupXSS(ctx *via.Ctx) {
	p.Map.ShowPopup(ctx, "info", maplibre.At(1, 2), `</script><img onerror="alert(1)">`)
}
func (p *mapActionPage) ShowPopupOpts(ctx *via.Ctx) {
	p.Map.ShowPopup(ctx, "info", maplibre.At(1, 2), "x",
		maplibre.WithoutCloseButton(), maplibre.WithoutCloseOnClick(),
		maplibre.PopupMaxWidth("240px"), maplibre.PopupClass("card", "lg"))
}
func (p *mapActionPage) ClosePopup(ctx *via.Ctx) { p.Map.ClosePopup(ctx, "info") }

// Data
func (p *mapActionPage) SetGeoJSON(ctx *via.Ctx) error {
	return p.Map.SetGeoJSON(ctx, "pts", maplibre.FeatureCollection(
		maplibre.PointFeature(1, 2, map[string]any{"name": "x"})))
}
func (p *mapActionPage) AddSource(ctx *via.Ctx) error {
	return p.Map.AddGeoJSONSource(ctx, "pts", maplibre.FeatureCollection())
}
func (p *mapActionPage) AddCircleLayer(ctx *via.Ctx) error {
	return p.Map.AddLayer(ctx, maplibre.CircleLayer("dots", "pts",
		maplibre.Paint("circle-radius", 6), maplibre.Paint("circle-color", "#e55")))
}
func (p *mapActionPage) SetPaint(ctx *via.Ctx) error {
	return p.Map.SetPaintProperty(ctx, "dots", "circle-color", "#00f")
}
func (p *mapActionPage) SetPaintBad(ctx *via.Ctx) error {
	return p.Map.SetPaintProperty(ctx, "dots", "circle-color", make(chan int))
}
func (p *mapActionPage) ShowLayer(ctx *via.Ctx) { p.Map.SetLayerVisibility(ctx, "dots", true) }
func (p *mapActionPage) HideLayer(ctx *via.Ctx) { p.Map.SetLayerVisibility(ctx, "dots", false) }
func (p *mapActionPage) RemoveLayerSource(ctx *via.Ctx) {
	p.Map.RemoveLayerAndSource(ctx, "dots", "pts")
}

// Lifecycle
func (p *mapActionPage) SetStyle(ctx *via.Ctx) {
	p.Map.SetStyle(ctx, "https://tiles.example/s.json")
}
func (p *mapActionPage) Resize(ctx *via.Ctx)  { p.Map.Resize(ctx) }
func (p *mapActionPage) Dispose(ctx *via.Ctx) { p.Map.Dispose(ctx) }
func (p *mapActionPage) DisposeTwice(ctx *via.Ctx) {
	p.Map.Dispose(ctx)
	p.Map.Dispose(ctx)
}
func (p *mapActionPage) CallEscape(ctx *via.Ctx) error {
	return p.Map.Call(ctx, "setMaxZoom", 18)
}
func (p *mapActionPage) CallBad(ctx *via.Ctx) error {
	return p.Map.Call(ctx, "panBy", make(chan int))
}

// fireMapAction boots a one-page app backed by mapActionPage, opens an SSE
// stream, fires the named action, and waits for a frame containing every
// needle. Mirrors the echarts plugin's runtime-test harness.
func fireMapAction(t *testing.T, action string, needles ...string) string {
	t.Helper()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[mapActionPage](app, "/")
	t.Cleanup(server.Close)

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	t.Cleanup(cancel)

	require.Equal(t, 200, tc.Action(action).Fire())
	return vt.AwaitFrame(t, frames, 2*time.Second, needles...)
}
