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
	actions int
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

func (b *stubBinder) ActionSlot(func()) string {
	i := b.actions
	b.actions++
	return strconv.Itoa(i)
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

func TestBytes_matchesStringForZeroCopyWriting(t *testing.T) {
	t.Parallel()
	// via writes the rendered tree straight to the ResponseWriter via Bytes()
	// to avoid a string copy; it must equal String().
	r := hcore.NewRenderer(&stubBinder{})
	r.Render(hcore.El("span", hcore.Str("x")))
	assert.Equal(t, r.String(), string(r.Bytes()), "Bytes() must equal String()")
}

func TestWriteEscapedAndWriteString_distinguishRawFromEscaped(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	r.WriteString("<b>")  // raw, caller pre-escaped
	r.WriteEscaped("<b>") // must be escaped
	assert.Equal(t, "<b>&lt;b&gt;", r.String())
}

// Dyn's node is a body node, never an attribute — El must route it into the
// tag's children even when it renders content that looks attribute-shaped.
func TestDyn_rendersInTheElementBody(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	r.Render(hcore.El("span", hcore.Dyn(func(r *hcore.Renderer) { r.WriteString("dyn") })))
	assert.Equal(t, "<span>dyn</span>", r.String())
}

// DynAttr satisfies Attr (isAttr), so El must route it into the opening tag —
// this is the seam OnClick and friends use to claim an action id at render
// time without El special-casing them by concrete type.
func TestDynAttr_rendersInTheOpeningTag(t *testing.T) {
	t.Parallel()
	r := hcore.NewRenderer(&stubBinder{})
	attr := hcore.DynAttr(func(r *hcore.Renderer) { r.WriteString(` data-x="1"`) })
	r.Render(hcore.El("span", attr, hcore.Str("body")))
	assert.Equal(t, `<span data-x="1">body</span>`, r.String())
}
