package via_test

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

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
type numComp struct{ n via.Signal[int] }

func (c *numComp) View() h.H { return h.Div(c.n.Display()) }

// The page-level data-signals declaration must carry an ordinary numeric signal
// verbatim — if the common case regressed, the client would hydrate nothing.
func TestDataSignals_declaresNumericSignalForHydration(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Register(numComp{})).Get("/")

	assert.Contains(t, body, `data-signals='{"n":0}'`, "numeric signal declaration missing/malformed")
}

// nameComp is a string signal plus a no-op action. The action lets a client
// round-trip an arbitrary string for the signal, which is reflected back into
// the page-level data-signals declaration on the response.
type nameComp struct{ name via.Signal[string] }

// Touch mutates the round-tripped value so the render changes and a patch (not a
// 204) is returned, letting the test inspect how the value is reflected.
func (c *nameComp) Touch(ctx *via.Ctx) { c.name.Set(c.name.Get() + "!") }
func (c *nameComp) View() h.H {
	return h.Div(h.Button(via.On("click", c.Touch), h.Str("x")), c.name.Display())
}

// A string signal value is attacker-influenced — it round-trips through the
// client. The data-signals declaration sits inside a single-quoted attribute,
// so a raw apostrophe in the value would close the attribute early and let the
// attacker graft a live data-on-load Datastar expression onto #root (XSS). A
// hydrated apostrophe must come back entity-encoded, never raw — asserted here
// against the real HTTP response, not the internal serializer.
func TestStringSignal_cannotBreakOutOfDataSignalsAttribute(t *testing.T) {
	t.Parallel()
	// Echo the breakout payload back as the signal's own slot; the request shape
	// (one slot, at field offset 0) matches what the GET page declares, so
	// dispatch proceeds.
	payload := `{"name":"' data-on-load='alert(document.cookie)"}`
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
func (f *boundForm) Save(ctx *via.Ctx) { f.Name.Set(f.Name.Get() + "!") }
func (f *boundForm) View() h.H {
	return h.Div(
		h.Input(f.Name.Bind()),
		h.Button(via.On("click", f.Save), h.Str("save")),
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

// TestSignal_bareSetBeforeRenderIsSafe pins the bare-mutator contract: Set(v)
// with no ctx works before the signal was ever rendered (no bound pass yet) —
// it updates server memory and emits no patch, without panicking. Fails if Set
// grows a required ctx again or dereferences an unstamped binding.
func TestSignal_bareSetBeforeRenderIsSafe(t *testing.T) {
	t.Parallel()
	var s via.Signal[string]
	s.Set("hello")
	assert.Equal(t, "hello", s.Get())
}

// A Set on a signal the View never rendered updates server memory but can emit
// no patch — that silence must be loud: exactly one warning per signal, not
// zero (a mystery) and not one per call (noise). Sequential: it captures the
// global log output.
func TestSignal_setOnNeverRenderedSignalWarnsOnce(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	var s via.Signal[int]
	s.Set(1)
	s.Set(2)
	assert.Equal(t, 1, strings.Count(buf.String(), "never rendered"),
		"exactly one warning per unrendered signal")
	assert.Equal(t, 2, s.Get(), "the value still updates server memory")
}

// twoSignals writes one signal and leaves the other alone — the second stands
// in for an input the user is mid-edit.
type twoSignals struct {
	Written via.Signal[string]
	Left    via.Signal[string]
}

func (t *twoSignals) Fill(ctx *via.Ctx) { t.Written.Set("ada") }

func (t *twoSignals) View() h.H {
	return h.Div(
		h.Input(t.Written.Bind()),
		h.Input(t.Left.Bind()),
		h.Button(via.On("click", t.Fill), h.Str("fill")),
	)
}

// A plain action's element patch declares ONLY the signals the action wrote.
// Re-declaring every slot would overwrite the whole client store on every
// action, so a value the user was mid-edit vanished on the next click.
func TestPlainAction_patchDeclaresOnlyTheSignalsItWrote(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(twoSignals{}))
	_, page := app.Get("/")
	assert.Contains(t, page, `"written":""`, "the GET first paint declares every slot")
	assert.Contains(t, page, `"left":""`, "the GET first paint declares every slot")

	status, frag := app.Action(0).Fire()
	assert.Equal(t, http.StatusOK, status, "the action patch is delivered")
	assert.Contains(t, frag, `"written":"ada"`, "the written signal is declared")
	assert.NotContains(t, frag, "left\":", "the untouched signal must not be re-declared")
}

// wizard is the conditional-Bind shape: exactly one of Name/Email is rendered
// per step, so their slots can only stay distinct if a slot is the field's
// identity rather than the order it was first rendered in. The step indicator
// renders AFTER the input, so a render-order scheme hands the step's own slot
// to the input.
type wizard struct {
	Step  via.Signal[int]
	Name  via.Signal[string]
	Email via.Signal[string]
	note  via.State[string] // server state: renders the page live
}

func (w *wizard) Next(ctx *via.Ctx) { w.Step.Set(1) }
func (w *wizard) Save(ctx *via.Ctx) { w.note.Set("saved") }

func (w *wizard) View() h.H {
	if w.Step.Get() == 0 {
		return h.Div(
			h.Input(w.Name.Bind()),
			h.Button(via.On("click", w.Next), h.Str("next")),
			w.Step.Display(),
			w.note.Display(),
		)
	}
	return h.Div(
		h.Input(w.Email.Bind()),
		h.Button(via.On("click", w.Save), h.Str("save")),
		w.Step.Display(),
		w.note.Display(),
		h.Str("name="+w.Name.Get()+" email="+w.Email.Get()),
	)
}

// bindSlots lists every data-bind slot in document order.
func bindSlots(markup string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`data-bind="([^"]*)"`).FindAllStringSubmatch(markup, -1) {
		out = append(out, m[1])
	}
	return out
}

// A signal whose Bind is conditional must never inherit a slot another signal
// already owns. On a streaming page the hydrator table is keyed by slot, so an
// aliased slot posts the user's input into the WRONG FIELD.
func TestSignal_conditionalBindKeepsItsOwnSlotOnALivePage(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(wizard{}))
	conn := app.Connect()

	_, step0 := app.Get("/")
	nameSlot := bindSlots(step0)[0]

	status, _ := app.Action(0).Over(conn).Body(`{"` + nameSlot + `":"Ada"}`).Fire()
	require.Equal(t, http.StatusNoContent, status, "the live action acks; the push carries the render")

	step1 := conn.Await("name=Ada")
	emailSlot := bindSlots(step1)[0]
	assert.NotEqual(t, nameSlot, emailSlot, "the email input must not be seated on the name's slot")

	// Save, with the client store the browser is actually holding: the name it
	// typed on step 1, under the slot that input had. Nothing has been typed
	// into the email box yet, so an aliased slot posts the name into Email.
	status, _ = app.Action(0).Over(conn).Body(`{"` + nameSlot + `":"Ada"}`).Fire()
	require.Equal(t, http.StatusNoContent, status)

	line := conn.Await("saved")
	assert.Contains(t, line, "name=Ada email=<", "the posted slot must write its own field, never the email's")
}

// The same aliasing on a plain page shows up client-side: the instance is
// fresh per request, so the wrong-field write lands in the Datastar store — the
// step-2 input would render bound to the slot still holding step 1's name.
func TestSignal_conditionalBindKeepsItsOwnSlotOnAPlainPage(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(plainWizard{}))
	_, page := app.Get("/")
	nameSlot := bindSlots(page)[0]

	status, frag := app.Action(0).Body(`{"` + nameSlot + `":"Ada"}`).Fire()
	require.Equal(t, http.StatusOK, status)
	emailSlot := bindSlots(frag)[0]
	assert.NotEqual(t, nameSlot, emailSlot, "the email input must not be seated on the name's slot")
	assert.NotContains(t, frag, "Ada", "and so must not render carrying the name the user typed")
}

// A plain action's patch declares the dirty slots — plus any slot the
// pre-action render did not carry. Without that, an input that appears for the
// first time in the response ships no declaration at all and the client either
// has no value for it or, worse, a stale one left by whatever held the slot.
func TestPlainAction_patchSeedsAnInputThatJustAppeared(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(plainWizard{}))
	_, page := app.Get("/")

	_, frag := app.Action(0).Body(`{"` + bindSlots(page)[0] + `":"Ada"}`).Fire()
	emailSlot := bindSlots(frag)[0]
	assert.Contains(t, frag, `"`+emailSlot+`":""`, "the newly-appearing input must be seeded")
}

// plainWizard is wizard without the State field, so the page stays
// plain and the step round-trips through the client store alone.
type plainWizard struct {
	Step  via.Signal[int]
	Name  via.Signal[string]
	Email via.Signal[string]
}

func (w *plainWizard) Next(ctx *via.Ctx) { w.Step.Set(1) }

func (w *plainWizard) View() h.H {
	if w.Step.Get() == 0 {
		return h.Div(h.Input(w.Name.Bind()), h.Button(via.On("click", w.Next), h.Str("next")), w.Step.Display())
	}
	return h.Div(h.Input(w.Email.Bind()), w.Step.Display())
}

// boxed reaches its signal through a pointer field, so the handle lives outside
// the composition struct and has no field offset to name itself by.
type boxed struct{ sig *via.Signal[string] }

func (b *boxed) View() h.H { return h.Div(h.Input(b.sig.Bind())) }

// A signal behind a pointer (or slice) field has no offset within the
// composition, so it falls back to the documented render-order slot rather
// than colliding with the field at offset 0.
func TestSignal_behindAPointerFieldFallsBackToARenderOrderSlot(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Register(boxed{sig: &via.Signal[string]{}})).Get("/")

	assert.Equal(t, []string{"s0"}, bindSlots(body), "a pointer-held signal keeps the render-order slot")
}

// --- slot scope and offset fallback ---

type valueReceiverView struct{ S via.Signal[int] }

// A value receiver binds a STACK COPY: every signal offsets from the wrong
// base and silently drops back to a render-order slot, losing the
// conditional-render safety the offsets exist for.
func (v valueReceiverView) View() h.H { return h.Div(v.S.Display()) }

func TestSignal_valueReceiverViewWarnsOnSlotFallback(t *testing.T) {
	via.ResetSlotFallbackWarningForTest()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	app := vt.Serve(t, via.Register(valueReceiverView{}))
	_, page := app.Get("/")
	assert.Contains(t, page, `data-text="$s"`, "the copy inherits the field name, but the write lands on a discarded struct")
	assert.Contains(t, logs.String(), "not addressable inside its composition")
}

// --- field-named slots and Signal.Ref (change 5) ---

// refChild calls Ref BEFORE the signal is Bound anywhere, which is the whole
// point: a field-held signal is named before the View runs, so a raw Datastar
// expression can reference it from anywhere in the tree.
type refChild struct{ Draft via.Signal[string] }

func (c *refChild) View() h.H {
	return h.Div(h.Data("show", c.Draft.Ref()), h.Input(c.Draft.Bind()))
}

type refPage struct {
	Count via.Signal[int]
	Chat  refChild
}

func (p *refPage) View() h.H {
	return h.Div(h.Data("show", p.Count.Ref()), p.Count.Display(), via.Embed(p.Chat))
}

// A slot is the Go FIELD name, so a user writing a raw Datastar expression
// reads it off the struct instead of reverse-engineering an opaque field offset
// out of the rendered HTML. An embed's slots carry its field path.
func TestSignal_slotIsTheFieldNameAndRefMatchesIt(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Register(refPage{})).Get("/")

	assert.Contains(t, body, `data-show="$count"`, "Ref names the root field, ahead of any Bind/Display")
	assert.Contains(t, body, `data-text="$count"`, "and Display agrees with it")
	assert.Contains(t, body, `data-show="$chat__draft"`, "an embed's signal is prefixed by its FIELD path")
	assert.Contains(t, body, `data-bind="chat__draft"`)
	assert.NotContains(t, body, `"f0"`, "opaque offset slots are gone")
}

// The embed prefix must survive a live push, which re-renders the child with no
// parent in scope — so the prefix rides on the instance, not on a parent walk.
type refLiveChild struct {
	Draft via.Signal[string]
	beat  via.State[int]
}

func (c *refLiveChild) OnInit(ctx *via.Ctx) error {
	ctx.Tick(5*time.Millisecond, c.tick)
	return nil
}
func (c *refLiveChild) tick(ctx *via.Ctx) { c.beat.Set(c.beat.Get() + 1) }
func (c *refLiveChild) View() h.H         { return h.Div(h.Input(c.Draft.Bind()), c.beat.Display()) }

type refLivePage struct{ Room refLiveChild }

func (p *refLivePage) View() h.H { return h.Div(via.Embed(p.Room)) }

func TestSignal_embedFieldPrefixSurvivesALivePush(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(refLivePage{}))
	_, page := app.Get("/")
	require.Contains(t, page, `data-bind="room__draft"`)

	conn := app.Connect()
	defer conn.Close()
	assert.Contains(t, conn.Await(`data-bind="room__draft"`), `data-bind="room__draft"`,
		"the push must mint the same field-path slot the first paint did")
}
