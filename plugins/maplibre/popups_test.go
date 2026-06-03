package maplibre_test

import (
	"strings"
	"testing"

	"github.com/go-via/via/plugins/maplibre"
	"github.com/stretchr/testify/assert"
)

func TestShowPopup_opensAKeyedTextPopupAtALocation(t *testing.T) {
	t.Parallel()
	// The interactive-dialog pattern: react to a click by showing an info
	// bubble at a point, driven from Go.
	frame := fireMapAction(t, "ShowPopup", "new maplibregl.Popup(")
	assert.Contains(t, frame, "setLngLat([-122.42,37.77])", "at the given LngLat")
	assert.Contains(t, frame, `.setText("Hello there")`, "with plain-text content (the XSS-safe sink)")
	assert.Contains(t, frame, "addTo(_e.m)", "added to the map")
	assert.Contains(t, frame, `_pp["info"]=`, "stored under its id for later ClosePopup")
	assert.Contains(t, frame, "_e.popups||(_e.popups={})",
		"the popup registry is lazily created so the initJS registry literal is untouched")
	// When the user closes the popup themselves (× button or map click), the
	// registry key must be dropped so it doesn't hold a stale reference.
	assert.Contains(t, frame, `_p.on('close',function(){delete _pp["info"]})`,
		"a user-initiated close must clean up the registry entry")
}

func TestShowPopupHTML_usesTheTrustedHTMLSink(t *testing.T) {
	t.Parallel()
	frame := fireMapAction(t, "ShowPopupHTML", "new maplibregl.Popup(")
	// Content is unicode-escaped by mustJSON for safe <script> transport (the
	// browser decodes it back), so assert the setHTML SINK, not the literal.
	assert.Contains(t, frame, ".setHTML(", "HTML content uses the setHTML sink")
	assert.Contains(t, frame, `trusted`, "carrying the markup")
	assert.NotContains(t, frame, ".setText(", "HTML mode must not also setText")
}

func TestShowPopupHTML_composesStructuredContentWithHBuilders(t *testing.T) {
	t.Parallel()
	// The point of taking h.H: build popup bodies with the same h.* builders
	// as the rest of the view, not a hand-concatenated HTML string.
	frame := fireMapAction(t, "ShowPopupStructured", "new maplibregl.Popup(")
	assert.Contains(t, frame, ".setHTML(", "structured content uses the HTML sink")
	assert.Contains(t, frame, "Title", "the rendered node's heading must be present")
	assert.Contains(t, frame, "body text", "and its body")
	// The h3/p TAGS must survive rendering. They're real HTML from h.H3/h.P,
	// unicode-escaped by mustJSON for transport (<h3> -> <h3>), which
	// proves the h.H was actually Render()ed to structured HTML, not stringified.
	assert.Contains(t, frame, `\u003ch3\u003e`, "the h3 element must be rendered")
	assert.Contains(t, frame, `\u003cp\u003e`, "the p element must be rendered")
}

func TestShowPopupHTML_hTextEscapesUserContentSoItCannotBreakOut(t *testing.T) {
	t.Parallel()
	// The safety win of h.H over a raw string: h.T escapes the content, so even
	// a </script> in user data can't break out of the inline script (and
	// mustJSON unicode-escapes for transport on top of that).
	frame := fireMapAction(t, "ShowPopupEscaped", "new maplibregl.Popup(")
	// Distinguish h.T escaping from mere JSON escaping: h.T turns the user's
	// '<' into '&lt;' BEFORE mustJSON, so mustJSON escapes the '&' and the frame
	// shows the entity form &lt;. Raw content (no h.T) would instead let
	// mustJSON escape the '<' directly to <b>evil — which must NOT
	// appear. This is exactly the assertion that fails if h.T is bypassed.
	assert.Contains(t, frame, `\u0026lt;`, "h.T must escape the user's '<' to '&lt;' before transport")
	assert.NotContains(t, frame, `\u003cb\u003eevil`, "the user's <b> must not survive as a real tag")
}

func TestShowPopupHTML_nilContentIsASafeEmptyPopup(t *testing.T) {
	t.Parallel()
	// A nil h.H must not panic (nil.Render) — it renders an empty body.
	frame := fireMapAction(t, "ShowPopupNilHTML", "new maplibregl.Popup(")
	assert.Contains(t, frame, `.setHTML("")`, "nil content renders as an empty popup, not a crash")
}

func TestShowPopup_escapesScriptBreakoutInTextContent(t *testing.T) {
	t.Parallel()
	// The content is inlined into a <script>; a </script> in it must be
	// unicode-escaped by mustJSON, and setText is the safe DOM sink.
	frame := fireMapAction(t, "ShowPopupXSS", "new maplibregl.Popup(")
	assert.NotContains(t, frame, "</script><img", "the breakout must be unicode-escaped")
	assert.Contains(t, frame, ".setText(", "user content must go through the XSS-safe setText sink")
}

func TestShowPopup_replacesSameIDInsteadOfStacking(t *testing.T) {
	t.Parallel()
	// Re-showing the same id must replace, so repeated clicks don't pile up
	// popups — mirrors AddMarker's keyed-replace behavior.
	frame := fireMapAction(t, "ShowPopup", "new maplibregl.Popup(")
	assert.Contains(t, frame, `if(_pp["info"])_pp["info"].remove()`,
		"an existing same-id popup must be removed before the new one is added")
	assert.Less(t, strings.Index(frame, `_pp["info"].remove()`), strings.Index(frame, "new maplibregl.Popup("),
		"the remove must come before constructing the replacement")
}

func TestClosePopup_removesTheKeyedPopup(t *testing.T) {
	t.Parallel()
	frame := fireMapAction(t, "ClosePopup", "_e.popups")
	assert.Contains(t, frame, ".remove()", "ClosePopup must remove the popup")
	assert.Contains(t, frame, `delete _pp["info"]`, "and drop it from the registry")
	// Closing a popup that was never opened (or already closed) must be a safe
	// no-op — guard on the registry and the entry both existing.
	assert.Contains(t, frame, "_e&&_e.popups", "must guard the popup registry existing")
	assert.Contains(t, frame, "if(_p){", "the remove/delete must be guarded by the popup existing")
}

func TestShowPopup_optionsConfigureThePopupChrome(t *testing.T) {
	t.Parallel()
	frame := fireMapAction(t, "ShowPopupOpts", "new maplibregl.Popup(")
	assert.Contains(t, frame, `"closeButton":false`, "WithoutCloseButton")
	assert.Contains(t, frame, `"closeOnClick":false`, "WithoutCloseOnClick")
	assert.Contains(t, frame, `"maxWidth":"240px"`, "PopupMaxWidth")
	assert.Contains(t, frame, `"className":"card lg"`, "PopupClass joins parts with a space")
}

func TestPopupClass_panicsOnWhitespaceInASinglePart(t *testing.T) {
	t.Parallel()
	// "a b" as one arg would silently become two classes — surface it eagerly,
	// like WithClass.
	assert.Panics(t, func() { maplibre.PopupClass("a b") })
}

func TestShowPopup_doesNotChangeTheInitRegistryLiteral(t *testing.T) {
	t.Parallel()
	// Popups lazily create _e.popups, so the initJS registry assignment must
	// still be exactly {m:_m,ro:_ro,markers:{}} — marker/ordering tests depend
	// on that literal.
	html := render(t, maplibre.NewMap(maplibre.WithElementID("m")))
	assert.Contains(t, html, "={m:_m,ro:_ro,markers:{}}",
		"the registry literal must be unchanged by the popup feature")
}
