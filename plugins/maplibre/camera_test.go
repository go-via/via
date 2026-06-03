package maplibre_test

import "testing"

func TestMapAPI_FlyTo_emitsCurvedFlightWithLngLatCenter(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "FlyTo", "flyTo", `"center":[-122.42,37.77]`, `"zoom":12`)
}

func TestMapAPI_EaseTo_emitsEasedMove(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "EaseTo", "easeTo", `"center":[2.35,48.85]`)
}

func TestMapAPI_JumpTo_emitsInstantMove(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "JumpTo", "jumpTo", `"center":[139.69,35.69]`)
}

func TestMapAPI_SetCenter_emitsLngLatArray(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "SetCenter", "setCenter([-0.12,51.5])")
}

func TestMapAPI_SetZoom_emitsZoomCall(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "SetZoom", "setZoom(7)")
}

func TestMapAPI_SetPitch_emitsPitchCall(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "SetPitch", "setPitch(60)")
}

func TestMapAPI_SetBearing_emitsBearingCall(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "SetBearing", "setBearing(90)")
}

func TestMapAPI_FitBounds_emitsSouthWestNorthEastBoxWithPadding(t *testing.T) {
	t.Parallel()
	// Assert the padding too: a bounds-only needle would still pass if the
	// padding were dropped, silently fitting flush to the viewport edges.
	fireMapAction(t, "FitBounds", "fitBounds([[-10,40],[5,55]],{padding:40})")
}
