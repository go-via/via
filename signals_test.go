package via_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attrValue extracts the value of a named HTML attribute from a rendered body.
func attrValue(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(name + `="([^"]*)"`).FindStringSubmatch(body)
	require.Len(t, m, 2, "attribute %s not found", name)
	return m[1]
}

// numComp renders a single client-resident numeric signal, so the page-level
// data-signals declaration is non-empty (the server-state counter declares none).
type numComp struct{ n via.Counter }

func (c *numComp) View() h.H { return h.Div(c.n.Display()) }

// The page-level data-signals declaration must carry an ordinary numeric signal
// verbatim — if the common case regressed, the client would hydrate nothing.
func TestDataSignals_declaresNumericSignalForHydration(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Register(numComp{})).Get("/")

	assert.Contains(t, body, `data-signals='{"s0":0}'`, "numeric signal declaration missing/malformed")
}

// nameComp is a string signal plus a no-op action. The action lets a client
// round-trip an arbitrary string for the signal, which is reflected back into
// the page-level data-signals declaration on the response.
type nameComp struct{ name via.Signal[string] }

// Touch mutates the round-tripped value so the render changes and a patch (not a
// 204) is returned, letting the test inspect how the value is reflected.
func (c *nameComp) Touch(ctx *via.Ctx) { c.name.Set(ctx, c.name.Get()+"!") }
func (c *nameComp) View() h.H {
	return h.Div(h.Button(via.OnClick(c.Touch), h.Str("x")), c.name.Display())
}

// A string signal value is attacker-influenced — it round-trips through the
// client. The data-signals declaration sits inside a single-quoted attribute,
// so a raw apostrophe in the value would close the attribute early and let the
// attacker graft a live data-on-load Datastar expression onto #root (XSS). A
// hydrated apostrophe must come back entity-encoded, never raw — asserted here
// against the real HTTP response, not the internal serializer.
func TestStringSignal_cannotBreakOutOfDataSignalsAttribute(t *testing.T) {
	t.Parallel()
	// Echo the breakout payload back as the s0 value; the request shape (one
	// signal slot, s0) matches what the GET page declares, so dispatch proceeds.
	payload := `{"s0":"' data-on-load='alert(document.cookie)"}`
	_, body := vt.Serve(t, via.Register(nameComp{})).Action(0).Body(payload).Fire()

	assert.NotContains(t, body, `' data-on-load='`, "raw apostrophe survived into the response — attribute breakout possible")
	assert.Contains(t, body, "&#39;", "apostrophe was not entity-encoded")
}

// greeting is a pure client-side reactive form: a text input two-way bound to a
// Signal and the same Signal displayed live elsewhere. No action, no server
// round-trip — Datastar updates the display as the user types.
type greeting struct{ Name via.Signal[string] }

func (g *greeting) View() h.H {
	return h.Div(
		h.Input(g.Name.Bind(), h.RawAttr("placeholder", "name")),
		h.P(h.Str("Hello, "), g.Name.Display(), h.Str("!")),
	)
}

// A signal that is both Bound to an input and Displayed must resolve to ONE
// shared wire name, or the two-way binding and the live display reference
// different signals and never update together. The data-bind value must equal
// the data-text signal (minus the $ sigil), and the name must be declared once
// for hydration.
func TestSignal_bindAndDisplayShareOneWireName(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Register(greeting{})).Get("/")

	bindSlot := attrValue(t, body, "data-bind")
	assert.NotEmpty(t, bindSlot, "input data-bind must not be empty")

	textExpr := attrValue(t, body, "data-text")
	assert.Equal(t, "$"+bindSlot, textExpr, "display must bind the same signal the input does")

	assert.Contains(t, body, `data-signals='{"`+bindSlot+`":""}'`,
		"the shared signal must be declared once for hydration")
}

// displayFirst Displays the signal before Binding it, so first-use order is the
// reverse of greeting. The shared name must be order-independent — the handle's
// identity, not whichever render happens first.
type displayFirst struct{ V via.Signal[string] }

func (d *displayFirst) View() h.H {
	return h.Div(d.V.Display(), h.Input(d.V.Bind()))
}

func TestSignal_sharedNameIsOrderIndependent(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Register(displayFirst{})).Get("/")
	textExpr := attrValue(t, body, "data-text")
	bindSlot := attrValue(t, body, "data-bind")
	assert.Equal(t, "$"+bindSlot, textExpr, "display and bind must share one name regardless of source order")
}

// boundForm rounds a Bound+Displayed signal through an action POST. The response
// fragment must reflect the value the client typed (not a zero reset) and keep
// the SAME wire name the GET page served, or the live binding desyncs after the
// first action.
type boundForm struct{ Name via.Signal[string] }

// Save appends to the bound value so the render changes — otherwise an action
// that leaves the View identical returns 204 (no patch) and there is nothing to
// assert about the response.
func (f *boundForm) Save(ctx *via.Ctx) { f.Name.Set(ctx, f.Name.Get()+"!") }
func (f *boundForm) View() h.H {
	return h.Div(
		h.Input(f.Name.Bind()),
		h.Button(via.OnClick(f.Save), h.Str("save")),
		h.P(f.Name.Display()),
	)
}

func TestSignal_boundValueRoundTripsAndSlotStaysStableAcrossPost(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(boundForm{}))

	_, page := app.Get("/")
	getSlot := attrValue(t, page, "data-bind")

	status, frag := app.Action(0).Body(`{"` + getSlot + `":"Ada"}`).Fire()
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, getSlot, attrValue(t, frag, "data-bind"), "wire name must be stable across the POST")
	assert.Contains(t, frag, "Ada", "response must reflect the value the client typed, not a zero reset")
}

// Counter is a numeric Signal with an Op(ctx) accessor for the verbs, keeping
// Add/Inc/Dec off the bare Signal surface.
func TestCounter_opAppliesNumericVerbs(t *testing.T) {
	t.Parallel()
	var c via.Counter
	c.Op(nil).Add(5)
	c.Op(nil).Inc()
	c.Op(nil).Dec()
	assert.Equal(t, 5, c.Get())
}

// localComp renders a client-only Local signal.
type localComp struct{ note via.Local[string] }

func (c *localComp) View() h.H {
	return h.Div(h.Input(c.note.Bind()), c.note.Display())
}

// Local is a client-only signal: its wire name is underscore-prefixed (Datastar
// never POSTs it), it is declared for the client, and it is two-way bindable and
// displayable — but it exposes no server Get/Set (no server doorway).
func TestLocal_isClientOnlyUnderscoreSignal(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Register(localComp{})).Get("/")
	assert.Contains(t, body, `data-bind="_s`, "Local binds an underscore-prefixed (client-only) signal")
	assert.Contains(t, body, `data-text="$_s`, "Local displays the same underscore signal")
	assert.Contains(t, body, `data-signals='{"_s`, "Local is declared so the client owns it")
}
