package hcore_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/go-via/via/internal/hcore"
	"github.com/stretchr/testify/assert"
)

// stubBinder is a minimal Binder for exercising the renderer without via or h.
type stubBinder struct {
	nextSig int
	init    map[string]any
}

func (b *stubBinder) SignalName() string {
	s := "s" + strconv.Itoa(b.nextSig)
	b.nextSig++
	return s
}

func (b *stubBinder) DeclareSignal(string, any) {}

func (b *stubBinder) SignalInit(slot string) (any, bool) {
	v, ok := b.init[slot]
	return v, ok
}

func (b *stubBinder) Hydrator(string, func(json.RawMessage)) {}

func TestBinder_isExposedSoDynamicNodesCanClaimSlots(t *testing.T) {
	t.Parallel()
	// via's signal/action nodes reach the Binder through r.Binder(); the
	// renderer must hand back the exact binder it was built with.
	b := &stubBinder{}
	r := hcore.NewRenderer(b)
	assert.Same(t, b, r.Binder(), "Binder() did not return the injected binder")
}

func TestWriteEscapedAndWriteString_distinguishRawFromEscaped(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	r.WriteString("<b>")  // raw, caller pre-escaped
	r.WriteEscaped("<b>") // must be escaped
	assert.Equal(t, "<b>&lt;b&gt;", string(r.Bytes()))
}

// Dyn's node is a body node, never an attribute — El must route it into the
// tag's children even when it renders content that looks attribute-shaped.
func TestDyn_rendersInTheElementBody(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	r.Render(hcore.El("span", hcore.Dyn(func(r *hcore.Renderer) { r.WriteString("dyn") })))
	assert.Equal(t, "<span>dyn</span>", string(r.Bytes()))
}

// DynAttr satisfies Attr (isAttr), so El must route it into the opening tag —
// this is the seam On and friends use to claim an action id at render
// time without El special-casing them by concrete type.
func TestDynAttr_rendersInTheOpeningTag(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	attr := hcore.DynAttr(func(r *hcore.Renderer) { r.WriteString(` data-x="1"`) })
	r.Render(hcore.El("span", attr, hcore.Str("body")))
	assert.Equal(t, `<span data-x="1">body</span>`, string(r.Bytes()))
}

// El writes tag straight into "<" + tag + ">" with no escaping of its own — an
// unvalidated tag string is as much a breakout vector as an unvalidated
// attribute name is, and "div onclick=alert(1)" grafts a live inline handler
// onto the opening tag. It must be held to the same allowlist RawAttr uses.
func TestEl_rejectsTagNamesThatCanBreakOutOfTheOpeningTag(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{
		"div onclick=alert(1)",
		"div ",
		"",
		"1div",
		"naïve", // non-ASCII outside the allowlist
	} {
		assert.Panicsf(t, func() { hcore.El(tag) },
			"El(%q) must panic — an unvalidated tag name is an injection vector", tag)
	}
}

// The allowlist must still admit ordinary tags: lowercase, uppercase (custom
// elements are conventionally lower, but the class is [A-Za-z] not [a-z]),
// and a hyphenated custom-element name.
func TestEl_acceptsOrdinaryTagNames(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{"div", "DIV", "my-widget", "a"} {
		var got string
		assert.NotPanicsf(t, func() {
			r := hcore.NewRenderer(&stubBinder{})
			r.Render(hcore.El(tag))
			got = string(r.Bytes())
		}, "El(%q) must be accepted", tag)
		assert.Equal(t, "<"+tag+"></"+tag+">", got)
	}
}
