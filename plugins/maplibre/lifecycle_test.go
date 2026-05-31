package maplibre_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapAPI_SetStyle_emitsSetStyleWithURL(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "SetStyle", `setStyle("https://tiles.example/s.json")`)
}

func TestMapAPI_Resize_emitsResizeCall(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "Resize", "?.resize()")
}

func TestMapAPI_Dispose_freesInstanceObserverAndRegistrySlot(t *testing.T) {
	t.Parallel()
	// All three must happen or the map leaks: WebGL/listeners via remove(),
	// the observer via disconnect(), and the retained ref via delete. The
	// if(_e) guard is what makes a second Dispose a safe no-op.
	fireMapAction(t, "Dispose", "if(_e)", ".remove()", ".disconnect()", "delete window.__viaMaps")
}

func TestMapAPI_Dispose_isIdempotentAcrossRepeatCalls(t *testing.T) {
	t.Parallel()
	// Disposing twice must not error: the registry-presence guard short-
	// circuits the second teardown after the slot is deleted.
	fireMapAction(t, "DisposeTwice", "delete window.__viaMaps")
}

func TestMapAPI_Call_emitsArbitraryMethodWithJSONArgs(t *testing.T) {
	t.Parallel()
	fireMapAction(t, "CallEscape", "?.setMaxZoom(18)")
}

func TestMapAPI_Call_returnsErrorOnUnmarshallableArg(t *testing.T) {
	t.Parallel()
	frame := fireMapAction(t, "CallBad", "maplibre: marshal")
	assert.NotContains(t, frame, "panBy",
		"a marshal failure must abort before emitting the call")
}
