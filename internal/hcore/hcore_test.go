package hcore_test

import (
	"encoding/json"
	"testing"

	"github.com/go-via/via/internal/hcore"
	"github.com/stretchr/testify/assert"
)

// stubBinder is a minimal Binder for exercising the renderer without via or h.
type stubBinder struct{}

func (*stubBinder) DeclareSignal(string, any) {}

func (*stubBinder) Hydrator(string, func(json.RawMessage) bool) {}

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
	r.WriteString("<b>")
	r.WriteEscaped("<b>")
	assert.Equal(t, "<b>&lt;b&gt;", string(r.Bytes()))
}

func TestDyn_rendersInTheElementBody(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	r.Render(hcore.El("span", hcore.Dyn(func(r *hcore.Renderer) { r.WriteString("dyn") })))
	assert.Equal(t, "<span>dyn</span>", string(r.Bytes()))
}

func TestDynAttr_rendersInTheOpeningTag(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	attr := hcore.DynAttr(func(r *hcore.Renderer) { r.WriteString(` data-x="1"`) })
	r.Render(hcore.El("span", attr, hcore.Str("body")))
	assert.Equal(t, `<span data-x="1">body</span>`, string(r.Bytes()))
}

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
